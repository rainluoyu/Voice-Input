package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"voice_input/internal/i18n"
	"voice_input/internal/netapi"
	"voice_input/internal/search"
)

func TestPresentIsPureMetadataProjection(t *testing.T) {
	message := i18n.M("app.switch_dialogue", "hotkey", "Ctrl+Alt+A")
	original := State{TaskID: "same-task", Intent: Dialogue, Turn: 3, Question: "正在启动录音", Replying: true, Revision: 72,
		Stage: Clarify, Visible: true, RawText: "原始中文 raw", Text: "模型中文 result", Notice: message.Text(i18n.ZhCN), NoticeMessage: &message,
		HideAt: 123456, Pinned: true, Mode: "live", Hotkey: "Ctrl+Alt+A", Level: .5,
		Sources: []search.Hit{{Title: "来源保持中文", URL: "https://example.test"}}}
	before := original
	projected := Present(original, i18n.EN)
	if projected.Notice != message.Text(i18n.EN) || original.Notice != before.Notice {
		t.Fatal("notice was not purely projected")
	}
	projected.Notice = before.Notice
	if !reflect.DeepEqual(projected, before) {
		t.Fatal("projection changed task, timer, text or evidence")
	}
	for _, text := range []string{"正在启动录音", "仍在处理中；请稍候，或关闭浮窗取消", "app.busy"} {
		state := State{Notice: text, Question: text}
		if got := Present(state, i18n.EN); !reflect.DeepEqual(got, state) {
			t.Fatal("plain/model strings were guessed as application messages")
		}
	}
	empty := i18n.Message{}
	if Present(State{Notice: "stale", NoticeMessage: &empty}, i18n.EN).Notice != "" {
		t.Fatal("empty message code must display empty")
	}
}

func TestModelNoticesIdenticalToApplicationTextRemainOpaque(t *testing.T) {
	for _, status := range []string{"skip", "clarify"} {
		for _, text := range []string{i18n.M("app.busy").Text(i18n.ZhCN), i18n.M("app.recording_start").Text(i18n.ZhCN), "app.busy"} {
			t.Run(status+"/"+text, func(t *testing.T) {
				p := &replyProcessor{raw: "原文", result: Result{Status: status, Notice: text}}
				c := NewWithFactory(func(context.Context, string, Intent, func(Stage, float64)) (Processor, TaskOptions, error) {
					return p, TaskOptions{Locale: i18n.EN, Mode: "live", Timeout: time.Second}, nil
				}, &fakeInjector{}, time.Hour)
				defer c.Close()
				c.Toggle(Dialogue)
				c.Toggle(Dialogue)
				end := terminalState(t, c)
				if end.NoticeMessage != nil {
					t.Fatal("model result received application metadata")
				}
				presented := Present(end, i18n.EN)
				if status == "skip" && presented.Notice != text {
					t.Fatal("model notice translated")
				}
				if status == "clarify" && (presented.Question != text || presented.Notice != "") {
					t.Fatal("model clarification translated or copied into application notice")
				}
			})
		}
	}
}

func TestApplicationNoticeProductionAndClearing(t *testing.T) {
	gate := make(chan struct{})
	p := &replyProcessor{raw: "raw", result: Result{Status: "skip", Notice: "model-owned"}}
	c := NewWithFactory(func(ctx context.Context, _ string, _ Intent, _ func(Stage, float64)) (Processor, TaskOptions, error) {
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return p, TaskOptions{Locale: i18n.EN, Mode: "live", Timeout: time.Second}, nil
	}, &fakeInjector{}, time.Hour)
	defer c.Close()
	c.Toggle(Dictation)
	first := c.Snapshot()
	if first.NoticeMessage == nil || first.NoticeMessage.Code != "app.recording_start" {
		t.Fatal("recording start missing descriptor")
	}
	c.Toggle(Dialogue)
	switched := Present(c.Snapshot(), i18n.EN)
	if switched.NoticeMessage == nil || switched.NoticeMessage.Code != "app.switch_dictation" || !strings.Contains(switched.Notice, "Ctrl+Alt+R") {
		t.Fatal("wrong intent descriptor", switched)
	}
	assertNoHan(t, switched.Notice)
	c.Toggle(Dictation)
	if stopped := c.Snapshot(); stopped.Notice != "" || stopped.NoticeMessage != nil {
		t.Fatal("notice clear kept stale metadata")
	}
	c.Toggle(Dictation)
	busy := Present(c.Snapshot(), i18n.EN)
	if busy.NoticeMessage == nil || busy.NoticeMessage.Code != "app.busy" {
		t.Fatal("busy missing descriptor")
	}
	assertNoHan(t, busy.Notice)
	close(gate)
	end := terminalState(t, c)
	if end.Notice != "model-owned" || end.NoticeMessage != nil {
		t.Fatal("model overwrite kept application metadata", end)
	}
}

func TestApplicationFailuresUseTypedMetadataAndSafeFallback(t *testing.T) {
	for _, phase := range []string{"init", "microphone", "asr", "llm", "input"} {
		for _, typed := range []bool{false, true} {
			t.Run(phase+map[bool]string{false: "/unknown", true: "/typed"}[typed], func(t *testing.T) {
				cause := errors.New("sensitive-provider-body 中文秘密")
				if typed {
					cause = netapi.Fail("audio", "device")
				}
				p := &localeFailureProcessor{phase: phase, cause: cause}
				inj := &fakeInjector{}
				if phase == "input" {
					inj.insertErr = cause
				}
				c := NewWithFactory(func(context.Context, string, Intent, func(Stage, float64)) (Processor, TaskOptions, error) {
					var err error
					if phase == "init" {
						err = cause
					}
					return p, TaskOptions{Locale: i18n.EN, Mode: "live", Timeout: time.Second}, err
				}, inj, time.Hour)
				defer c.Close()
				c.Toggle(Dictation)
				if phase != "init" && phase != "microphone" {
					c.Toggle(Dictation)
				}
				end := waitState(t, c, func(s State) bool { return s.Stage == Error })
				if end.NoticeMessage == nil || strings.Contains(end.Notice, "sensitive") || strings.Contains(end.Notice, "秘密") {
					t.Fatal("untyped error leaked or metadata missing", end)
				}
				if typed && end.NoticeMessage.Code != "net.audio.device" {
					t.Fatal("typed descriptor lost", end)
				}
				assertNoHan(t, end.Notice)
				assertNoHan(t, Present(end, i18n.EN).Notice)
			})
		}
	}
}

type localeFailureProcessor struct {
	phase string
	cause error
}

func (p *localeFailureProcessor) Begin(context.Context) error {
	if p.phase == "microphone" {
		return p.cause
	}
	return nil
}
func (p *localeFailureProcessor) Transcribe(context.Context) (string, error) {
	if p.phase == "asr" {
		return "", p.cause
	}
	return "original speech", nil
}
func (p *localeFailureProcessor) Complete(context.Context, string) (Result, error) {
	if p.phase == "llm" {
		return Result{}, p.cause
	}
	return Result{Status: "insert", Text: "result"}, nil
}

func TestTaskLocalePersistsAcrossClarificationsAndCopy(t *testing.T) {
	var calls atomic.Int32
	requests := make(chan Conversation, 1)
	factoryLocales := make(chan i18n.Locale, 2)
	c := NewWithFactory(func(ctx context.Context, _ string, _ Intent, _ func(Stage, float64)) (Processor, TaskOptions, error) {
		index := calls.Add(1)
		locale := i18n.EN
		var processor Processor = ask("原始请求", "模型问题中文？")
		if index == 2 {
			locale = i18n.ZhCN
			processor = answer("补充中文", requests)
			processor.(*replyProcessor).result = Result{Status: "skip", Notice: "模型保持中文"}
		}
		factoryLocales <- TaskLocale(ctx, locale)
		return processor, TaskOptions{Locale: locale, Mode: "live", Timeout: time.Second}, nil
	}, &fakeInjector{}, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	waiting := terminalState(t, c)
	if <-factoryLocales != i18n.EN {
		t.Fatal("initial snapshot changed")
	}
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	end := waitState(t, c, func(s State) bool { return s.Stage == Skip && s.Turn == 2 })
	if <-factoryLocales != i18n.EN {
		t.Fatal("clarification did not inherit task language")
	}
	conversation := <-requests
	want := conversation.TranscriptForLocale(i18n.EN)
	copy, err := c.CopyText(end.TaskID)
	if err != nil || end.RawText != want || copy != want || !strings.HasPrefix(want, "Original speech: 原始请求") || !strings.Contains(want, "Question 1: 模型问题中文？\nSupplement 1: 补充中文") {
		t.Fatal("transcript display/copy differs", end, copy, err)
	}
	if waiting.TaskID != end.TaskID || conversation.Transcript() == want {
		t.Fatal("task identity or legacy Chinese compatibility changed")
	}
}

func assertNoHan(t *testing.T, text string) {
	t.Helper()
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			t.Fatalf("unexpected Chinese in English application text: %q", text)
		}
	}
}
