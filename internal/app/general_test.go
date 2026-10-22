package app

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type copyingInjector struct {
	fakeInjector
	copyMu  sync.Mutex
	copied  []string
	copyErr error
}

func (f *copyingInjector) Copy(text string) error {
	f.copyMu.Lock()
	defer f.copyMu.Unlock()
	f.copied = append(f.copied, text)
	return f.copyErr
}
func (f *copyingInjector) copies() []string {
	f.copyMu.Lock()
	defer f.copyMu.Unlock()
	return append([]string(nil), f.copied...)
}
func copyController(p Processor, inj *copyingInjector, enabled bool) *Controller {
	return NewWithFactory(func(context.Context, string, Intent, func(Stage, float64)) (Processor, TaskOptions, error) {
		return p, TaskOptions{AutoCopy: enabled, Timeout: time.Second}, nil
	}, inj, time.Hour)
}

func TestAutoCopyIndependentOfInput(t *testing.T) {
	for _, tc := range []struct {
		name       string
		result     Result
		llmErr     error
		inputErr   error
		copyErr    error
		disabled   bool
		stage      Stage
		want       []string
		copyCode   string
		injections int
	}{
		{name: "disabled", result: Result{Status: "insert", Text: "final"}, disabled: true, stage: Done, injections: 1},
		{name: "final replaces raw", result: Result{Status: "insert", Text: "final"}, stage: Done, want: []string{"original", "final"}, copyCode: "popup.copy.success", injections: 1},
		{name: "passthrough deduplicated", result: Result{Status: "insert", Text: "original"}, stage: Done, want: []string{"original"}, copyCode: "popup.copy.success", injections: 1},
		{name: "input failed", result: Result{Status: "insert", Text: "final"}, inputErr: errors.New("input failed"), stage: Error, want: []string{"original", "final"}, copyCode: "popup.copy.success"},
		{name: "unsupported input controls", result: Result{Status: "insert", Text: "a\tb"}, stage: Error, want: []string{"original", "a\tb"}, copyCode: "popup.copy.success"},
		{name: "LLM failed", llmErr: errors.New("provider private error"), stage: Error, want: []string{"original"}, copyCode: "popup.copy.success"},
		{name: "copy and LLM both failed", llmErr: errors.New("provider private error"), copyErr: errors.New("clipboard busy"), stage: Error, want: []string{"original"}, copyCode: "popup.copy.failed"},
		{name: "invalid protocol", result: Result{Status: "clarify", Text: "invalid", Notice: "question"}, stage: Error, want: []string{"original"}, copyCode: "popup.copy.success"},
		{name: "clarify question not copied", result: Result{Status: "clarify", Notice: "question"}, stage: Clarify, want: []string{"original"}, copyCode: "popup.copy.success"},
		{name: "skip keeps raw", result: Result{Status: "skip", Notice: "no output"}, stage: Skip, want: []string{"original"}, copyCode: "popup.copy.success"},
		{name: "copy failure does not block input", result: Result{Status: "insert", Text: "final"}, copyErr: errors.New("clipboard busy"), stage: Done, want: []string{"original", "final"}, copyCode: "popup.copy.failed", injections: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inj := &copyingInjector{fakeInjector: fakeInjector{insertErr: tc.inputErr}, copyErr: tc.copyErr}
			c := copyController(invalidProcessor{result: tc.result, err: tc.llmErr}, inj, !tc.disabled)
			defer c.Close()
			c.Toggle(Dialogue)
			c.Toggle(Dialogue)
			end := terminalState(t, c)
			if end.Stage != tc.stage || inj.calls() != tc.injections || !reflect.DeepEqual(inj.copies(), tc.want) {
				t.Fatalf("state=%+v injections=%d copies=%q want=%q", end, inj.calls(), inj.copies(), tc.want)
			}
			if tc.copyCode == "" {
				if end.CopyMessage != nil {
					t.Fatal("disabled automatic copy reported feedback")
				}
			} else if end.CopyMessage == nil || end.CopyMessage.Code != tc.copyCode {
				t.Fatalf("copy feedback=%+v want=%s", end.CopyMessage, tc.copyCode)
			}
			if tc.llmErr != nil && (end.NoticeMessage == nil || end.NoticeMessage.Code != "app.llm_failed") {
				t.Fatal("copy feedback replaced the task failure", end)
			}
			before := inj.copies()
			c.KeepOpen(end.TaskID)
			c.Presentation("live", "Ctrl+Alt+R", "Ctrl+Alt+A")
			if !reflect.DeepEqual(before, inj.copies()) {
				t.Fatal("presentation or pin repeated automatic copy")
			}
		})
	}
}

func TestAutoCopyKeepsRawWhenCompletionTimesOut(t *testing.T) {
	p := &heldProcessor{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}), atLLM: true}
	inj := &copyingInjector{}
	c := NewWithFactory(func(context.Context, string, Intent, func(Stage, float64)) (Processor, TaskOptions, error) {
		return p, TaskOptions{AutoCopy: true, Timeout: 40 * time.Millisecond}, nil
	}, inj, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	<-p.entered
	if !reflect.DeepEqual(inj.copies(), []string{"raw"}) {
		t.Fatal("ASR was not copied before completion")
	}
	end := terminalState(t, c)
	if end.Stage != Error || end.NoticeMessage == nil || end.NoticeMessage.Code != "app.timeout" {
		t.Fatal("processing deadline did not end the task", end)
	}
	close(p.release)
	<-p.finished
	time.Sleep(20 * time.Millisecond)
	if !reflect.DeepEqual(inj.copies(), []string{"raw"}) {
		t.Fatal("timed-out completion replaced the raw clipboard")
	}
}

func TestAutoCopyRejectsLateTaskResults(t *testing.T) {
	for _, atLLM := range []bool{false, true} {
		p := &heldProcessor{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}), atLLM: atLLM}
		inj := &copyingInjector{}
		c := copyController(p, inj, true)
		c.Toggle(Dialogue)
		c.Toggle(Dialogue)
		<-p.entered
		c.Close()
		c.Toggle(Dialogue)
		fresh := c.Snapshot()
		before := inj.copies()
		close(p.release)
		<-p.finished
		time.Sleep(20 * time.Millisecond)
		if !reflect.DeepEqual(before, inj.copies()) || c.Snapshot().TaskID != fresh.TaskID || c.Snapshot().CopyMessage != nil {
			t.Fatal("obsolete result copied into a newer task")
		}
		c.Close()
	}
}

type shutdownProcessor struct {
	invalidProcessor
	begun, restoring, release chan struct{}
}

func (p *shutdownProcessor) Begin(context.Context) error { close(p.begun); return nil }
func (p *shutdownProcessor) StopRecording(ctx context.Context) error {
	close(p.restoring)
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestShutdownWaitsForRecordingRestore(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		p := &shutdownProcessor{begun: make(chan struct{}), restoring: make(chan struct{}), release: make(chan struct{})}
		c := New(p, &fakeInjector{}, time.Hour)
		c.Toggle(Dictation)
		<-p.begun
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if timeout {
			cancel()
		}
		result := make(chan error, 1)
		go func() { result <- c.Shutdown(ctx) }()
		if timeout {
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatal("shutdown lost its cancellation bound", err)
			}
		} else {
			<-p.restoring
			select {
			case <-result:
				t.Fatal("shutdown returned before playback restoration")
			default:
			}
			close(p.release)
			if err := <-result; err != nil {
				t.Fatal(err)
			}
		}
		cancel()
	}
}

func TestTaskOptionsOverrideAutoHideAndKeepPin(t *testing.T) {
	c := NewWithFactory(func(context.Context, string, Intent, func(Stage, float64)) (Processor, TaskOptions, error) {
		return invalidProcessor{result: Result{Status: "insert", Text: "final"}}, TaskOptions{AutoHide: 60 * time.Millisecond}, nil
	}, &fakeInjector{}, time.Hour)
	defer c.Close()
	c.Toggle(Dictation)
	c.Toggle(Dictation)
	end := terminalState(t, c)
	remaining := time.Until(time.UnixMilli(end.HideAt))
	if remaining <= 0 || remaining > 100*time.Millisecond {
		t.Fatalf("configured task wait ignored: %s", remaining)
	}
	waitState(t, c, func(s State) bool { return !s.Visible })
	c.Toggle(Dictation)
	c.KeepOpen(c.Snapshot().TaskID)
	c.Toggle(Dictation)
	end = terminalState(t, c)
	time.Sleep(90 * time.Millisecond)
	if !c.Snapshot().Visible || end.HideAt != 0 || !end.Pinned {
		t.Fatal("configured timeout overrode keep-open interaction", end)
	}
}
