package app

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type replyProcessor struct {
	raw              string
	result           Result
	asrErr, startErr error
	requests         chan Conversation
	started          chan struct{}
}

func (p *replyProcessor) Begin(context.Context) error {
	if p.started != nil {
		close(p.started)
	}
	return p.startErr
}
func (p *replyProcessor) Transcribe(context.Context) (string, error)       { return p.raw, p.asrErr }
func (p *replyProcessor) Complete(context.Context, string) (Result, error) { return p.result, nil }
func (p *replyProcessor) CompleteConversation(_ context.Context, input Conversation) (Result, error) {
	if p.requests != nil {
		p.requests <- input.Clone()
	}
	return p.result, nil
}
func dialogueController(t *testing.T, processors ...*replyProcessor) (*Controller, *fakeInjector) {
	t.Helper()
	var calls atomic.Int32
	inj := &fakeInjector{}
	c := NewWithFactory(func(context.Context, string, Intent, func(Stage, float64)) (Processor, TaskOptions, error) {
		i := int(calls.Add(1)) - 1
		if i >= len(processors) {
			return nil, TaskOptions{}, errors.New("unexpected extra turn")
		}
		return processors[i], TaskOptions{Mode: "live", Hotkey: "Ctrl+Alt+R", Timeout: 35 * time.Millisecond}, nil
	}, inj, 35*time.Millisecond)
	t.Cleanup(c.Close)
	return c, inj
}
func ask(raw, question string) *replyProcessor {
	return &replyProcessor{raw: raw, result: Result{Status: "clarify", Notice: question}}
}
func answer(raw string, requests chan Conversation) *replyProcessor {
	return &replyProcessor{raw: raw, result: Result{Status: "insert", Text: "结合原请求和补充生成的结果。"}, requests: requests}
}

func TestClarificationWaitsAndReplyUsesOriginalRequest(t *testing.T) {
	requests := make(chan Conversation, 1)
	c, inj := dialogueController(t, ask("向他介绍一下 2025 年的 F1 世界冠军。", "车手冠军还是车队冠军？"), answer("车手冠军", requests))
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	waiting := waitState(t, c, func(s State) bool { return s.Stage == Clarify })
	if waiting.Question != "车手冠军还是车队冠军？" || waiting.HideAt != 0 || waiting.Text != "" || !waiting.Visible || !c.Busy() || inj.calls() != 0 {
		t.Fatal(waiting)
	}
	c.mu.Lock()
	oldContext := c.ctx
	c.mu.Unlock()
	// Both the prior processing deadline and normal result-hide period expire.
	time.Sleep(90 * time.Millisecond)
	if s := c.Snapshot(); s.Stage != Clarify || !s.Visible {
		t.Fatalf("question expired: %+v", s)
	}
	c.Reply(waiting.TaskID, waiting.Turn, false)
	recording := c.Snapshot()
	if recording.TaskID != waiting.TaskID || recording.Turn != waiting.Turn+1 || !recording.Replying || recording.Stage != Recording || recording.Question != waiting.Question {
		t.Fatal(recording)
	}
	// Explicitly replay an old turn's cancelled timeout and late audio callback.
	c.mu.Lock()
	c.finishTimeout(oldContext, waiting.TaskID, waiting.Turn)
	c.mu.Unlock()
	c.notify(oldContext, waiting.TaskID, waiting.Turn, Recording, 0.8)
	c.Reply(waiting.TaskID, waiting.Turn, false)
	if s := c.Snapshot(); s.Stage != Recording || s.Turn != recording.Turn || s.Level != 0 {
		t.Fatalf("old turn affected reply: %+v", s)
	}
	c.Reply(recording.TaskID, recording.Turn, true)
	end := waitState(t, c, func(s State) bool { return s.Stage == Done })
	input := <-requests
	if input.Original != waiting.RawText || len(input.Clarifications) != 1 || input.Clarifications[0] != (Clarification{Question: waiting.Question, Answer: "车手冠军"}) {
		t.Fatalf("context lost: %+v", input)
	}
	if end.TaskID != waiting.TaskID || end.Question != "" || end.Replying || end.HideAt == 0 || inj.calls() != 1 || !strings.Contains(end.RawText, "车手冠军") {
		t.Fatal(end)
	}
}

func TestMultipleRepliesAndStaleButtonActions(t *testing.T) {
	requests := make(chan Conversation, 2)
	second := ask("上海", "查今天还是明天？")
	second.requests = requests
	c, inj := dialogueController(t, ask("帮我整理天气介绍。", "查询哪个城市？"), second, answer("明天", requests))
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	first := waitState(t, c, func(s State) bool { return s.Stage == Clarify })
	c.Reply("old-task", first.Turn, false)
	if c.Snapshot().Stage != Clarify {
		t.Fatal("stale task started recording")
	}
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	waiting := waitState(t, c, func(s State) bool { return s.Stage == Clarify && s.Turn == 2 })
	firstReply := <-requests
	if len(firstReply.Clarifications) != 1 || firstReply.Clarifications[0].Answer != "上海" {
		t.Fatal(firstReply)
	}
	// A delayed stop click from turn 2 must not start answering its new question.
	c.Reply(waiting.TaskID, waiting.Turn, true)
	if c.Snapshot().Stage != Clarify {
		t.Fatal("late stop click started a new reply")
	}
	c.Reply(waiting.TaskID, waiting.Turn, false)
	c.Reply(waiting.TaskID, waiting.Turn, false)
	if s := c.Snapshot(); s.Stage != Recording || s.Turn != 3 {
		t.Fatal("duplicate start stopped the next recording", s)
	}
	c.Toggle(Dialogue)
	end := waitState(t, c, func(s State) bool { return s.Stage == Done })
	input := <-requests
	if input.Original != "帮我整理天气介绍。" || len(input.Clarifications) != 2 || input.Clarifications[0].Question != first.Question || input.Clarifications[1] != (Clarification{Question: waiting.Question, Answer: "明天"}) || inj.calls() != 1 {
		t.Fatalf("bad history: %+v", input)
	}
	if end.TaskID != first.TaskID || !strings.Contains(end.RawText, "补充 2：明天") {
		t.Fatal(end)
	}
}

func TestFailedReplyRecordingCanBeRetried(t *testing.T) {
	for _, failure := range []string{"startup", "asr", "empty"} {
		t.Run(failure, func(t *testing.T) {
			failed := answer("", nil)
			switch failure {
			case "startup":
				failed.startErr = errors.New("microphone failure")
			case "asr":
				failed.asrErr = errors.New("provider failure")
			}
			requests := make(chan Conversation, 1)
			c, inj := dialogueController(t, ask("查询天气。", "哪个城市？"), failed, answer("上海", requests))
			c.Toggle(Dialogue)
			c.Toggle(Dialogue)
			original := waitState(t, c, func(s State) bool { return s.Stage == Clarify })
			c.Toggle(Dialogue)
			if failure != "startup" {
				c.Toggle(Dialogue)
			}
			waiting := waitState(t, c, func(s State) bool { return s.Stage == Clarify && s.Turn == 2 })
			if waiting.Question != original.Question || waiting.RawText != original.RawText || waiting.HideAt != 0 || !strings.Contains(waiting.Notice, "重新录制") {
				t.Fatal(waiting)
			}
			c.Toggle(Dialogue)
			c.Toggle(Dialogue)
			waitState(t, c, func(s State) bool { return s.Stage == Done })
			input := <-requests
			if len(input.Clarifications) != 1 || input.Clarifications[0].Answer != "上海" || inj.calls() != 1 {
				t.Fatal(input)
			}
		})
	}
}

func TestClosingQuestionOrReplyStartsFreshTask(t *testing.T) {
	for _, recording := range []bool{false, true} {
		t.Run(map[bool]string{false: "waiting", true: "reply_recording"}[recording], func(t *testing.T) {
			processors := []*replyProcessor{ask("旧请求。", "旧问题？")}
			if recording {
				reply := answer("不应提交", nil)
				reply.started = make(chan struct{})
				processors = append(processors, reply)
			}
			processors = append(processors, answer("新的独立请求。", nil))
			c, inj := dialogueController(t, processors...)
			c.Toggle(Dialogue)
			c.Toggle(Dialogue)
			old := waitState(t, c, func(s State) bool { return s.Stage == Clarify })
			if recording {
				c.Toggle(Dialogue)
				<-processors[1].started
			}
			c.Close()
			c.Reply(old.TaskID, old.Turn, false)
			if s := c.Snapshot(); s.Stage != Cancelled || s.Visible || s.Question != "" || c.Busy() || inj.calls() != 0 {
				t.Fatal(s)
			}
			c.Toggle(Dialogue)
			c.Toggle(Dialogue)
			end := waitState(t, c, func(s State) bool { return s.Stage == Done })
			if end.TaskID == old.TaskID || end.RawText != "新的独立请求。" || end.Turn != 1 || inj.calls() != 1 {
				t.Fatalf("old conversation reused: %+v", end)
			}
		})
	}
}

func TestClarificationHistoryIsBounded(t *testing.T) {
	processors := make([]*replyProcessor, MaxClarifications+1)
	for i := range processors {
		processors[i] = ask("补充内容", "还需要哪个信息？")
	}
	c, inj := dialogueController(t, processors...)
	for i := 0; i <= MaxClarifications; i++ {
		c.Toggle(Dialogue)
		c.Toggle(Dialogue)
		end := terminalState(t, c)
		if i < MaxClarifications && end.Stage != Clarify {
			t.Fatal(end)
		}
		if i == MaxClarifications && (end.Stage != Error || !strings.Contains(end.Notice, "上限") || end.Question != "") {
			t.Fatal(end)
		}
	}
	if inj.calls() != 0 {
		t.Fatal("clarification text was submitted")
	}
}

func TestMockClarificationCanContinueOffline(t *testing.T) {
	inj := &fakeInjector{}
	c := New(Mock{Scenario: "clarify", Delay: time.Millisecond, RawText: "演示请求"}, inj, time.Second)
	defer c.Close()
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	waiting := waitState(t, c, func(s State) bool { return s.Stage == Clarify })
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	end := waitState(t, c, func(s State) bool { return s.Stage == Done })
	if end.TaskID != waiting.TaskID || end.Turn != 2 || end.Question != "" || !strings.HasPrefix(end.Text, "模拟续答：演示请求") || inj.calls() != 1 {
		t.Fatal(end)
	}
}

func TestConversationCopiesAndValidatesInput(t *testing.T) {
	c := Conversation{Original: "原请求", Clarifications: []Clarification{{Question: "哪个城市？", Answer: "上海"}}}
	copy := c.Clone()
	copy.Clarifications[0].Answer = "别处"
	if c.Clarifications[0].Answer != "上海" {
		t.Fatal("history aliased")
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Conversation{
		{}, {Original: "原请求", Clarifications: []Clarification{{Question: "问题", Answer: " "}}},
		{Original: "原请求", Clarifications: make([]Clarification, MaxClarifications+1)},
		{Original: "原请求", Clarifications: []Clarification{{Question: "问题", Answer: strings.Repeat("x", 32001)}}},
	} {
		if invalid.Validate() == nil {
			t.Fatal("invalid conversation accepted")
		}
	}
	tooLarge := Conversation{Original: strings.Repeat("x", 32000)}
	for range 4 {
		tooLarge.Clarifications = append(tooLarge.Clarifications, Clarification{Question: "问题", Answer: strings.Repeat("y", 32000)})
	}
	if tooLarge.Validate() == nil {
		t.Fatal("unbounded conversation accepted")
	}
}
