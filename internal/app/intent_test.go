package app

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOtherIntentCannotStopOrReplaceRecording(t *testing.T) {
	for _, intent := range []Intent{Dictation, Dialogue} {
		other := Dictation
		if intent == Dictation {
			other = Dialogue
		}
		c := New(Mock{Intent: intent, Delay: 10 * time.Millisecond}, &fakeInjector{}, time.Hour)
		c.Presentation("mock", "Ctrl+Shift+R", "Ctrl+Shift+A")
		c.Toggle(intent)
		first := c.Snapshot()
		c.Toggle(other)
		if s := c.Snapshot(); s.TaskID != first.TaskID || s.Turn != first.Turn || s.Intent != intent || s.Stage != Recording || !strings.Contains(s.Notice, "取消") {
			t.Fatal("wrong shortcut interrupted recording", s)
		}
		c.Toggle(intent)
		c.Toggle(other)
		end := waitState(t, c, func(s State) bool { return s.Stage == Done })
		if end.TaskID != first.TaskID || end.Intent != intent {
			t.Fatal("intent changed during processing", end)
		}
		c.Close()
		c.Toggle(other)
		if s := c.Snapshot(); s.Intent != other || s.TaskID == first.TaskID || s.Stage != Recording {
			t.Fatal("fresh task did not switch purpose", s)
		}
		c.Close()
	}
}
func TestDialogueIntentAndShortcutSurviveClarification(t *testing.T) {
	seen := make(chan Intent, 2)
	requests := make(chan Conversation, 1)
	var count atomic.Int32
	processors := []*replyProcessor{ask("原始请求", "哪个城市？"), answer("上海", requests)}
	c := NewWithFactory(func(_ context.Context, _ string, intent Intent, _ func(Stage, float64)) (Processor, TaskOptions, error) {
		seen <- intent
		i := int(count.Add(1)) - 1
		return processors[i], TaskOptions{Mode: "live", Hotkey: "Ctrl+Shift+A", Timeout: time.Second}, nil
	}, &fakeInjector{}, time.Hour)
	defer c.Close()
	c.Presentation("live", "Ctrl+Shift+R", "Ctrl+Shift+A")
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	pending := waitState(t, c, func(s State) bool { return s.Stage == Clarify })
	c.Toggle(Dictation)
	if s := c.Snapshot(); s.Stage != Clarify || s.TaskID != pending.TaskID || s.Intent != Dialogue || s.Hotkey != "Ctrl+Shift+A" {
		t.Fatal("wrong shortcut consumed question", s)
	}
	c.Toggle(Dialogue)
	c.Toggle(Dictation)
	if c.Snapshot().Stage != Recording {
		t.Fatal("wrong shortcut stopped supplement")
	}
	c.Toggle(Dialogue)
	end := waitState(t, c, func(s State) bool { return s.Stage == Done })
	if <-seen != Dialogue || <-seen != Dialogue || end.Intent != Dialogue || end.Hotkey != pending.Hotkey || len((<-requests).Clarifications) != 1 {
		t.Fatal("supplement intent lost", end)
	}
}
func TestDictationCannotEnterClarification(t *testing.T) {
	inj := &fakeInjector{}
	c := New(ask("帮我写一封请假邮件", "请假日期？"), inj, time.Hour)
	defer c.Close()
	c.Toggle(Dictation)
	c.Toggle(Dictation)
	end := terminalState(t, c)
	if end.Stage != Error || end.Question != "" || end.RawText == "" || inj.calls() != 0 {
		t.Fatal("dictation began dialogue", end)
	}
	c.Reply(end.TaskID, end.Turn, false)
	if c.Snapshot().Stage != Error {
		t.Fatal("dictation reply accepted")
	}
}
func TestPresentationUpdatesFutureKeysWhileResultVisible(t *testing.T) {
	c := New(Mock{}, &fakeInjector{}, time.Hour)
	defer c.Close()
	c.Toggle(Dictation)
	c.Toggle(Dictation)
	terminalState(t, c)
	c.Presentation("mock", "Ctrl+Shift+R", "Ctrl+Shift+A")
	c.Toggle(Dialogue)
	if s := c.Snapshot(); s.Intent != Dialogue || s.Hotkey != "Ctrl+Shift+A" {
		t.Fatal("new keys not applied", s)
	}
}
func TestMultilineResultIsPreservedForCopy(t *testing.T) {
	c := New(invalidProcessor{result: Result{Status: "insert", Text: "您好：\r\n\r\n  正文 📦"}}, &fakeInjector{}, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	end := terminalState(t, c)
	text, err := c.CopyText(end.TaskID)
	if err != nil || end.Stage != Done || text != "您好：\n\n  正文 📦" || end.Notice != "" {
		t.Fatal("multiline lost", end, err)
	}
}
