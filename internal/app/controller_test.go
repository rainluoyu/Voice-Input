package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeInjector struct {
	mu        sync.Mutex
	count     int
	insertErr error
}

func (f *fakeInjector) Prepare(ctx context.Context, _ string) (func() error, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if f.insertErr != nil {
		return nil, f.insertErr
	}
	return func() error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		f.count++
		return nil
	}, nil
}
func (f *fakeInjector) calls() int { f.mu.Lock(); defer f.mu.Unlock(); return f.count }
func waitState(t *testing.T, c *Controller, predicate func(State) bool) State {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state := c.Snapshot()
		if predicate(state) {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("state not reached: %+v", c.Snapshot())
	return State{}
}
func terminalState(t *testing.T, c *Controller) State {
	return waitState(t, c, func(s State) bool { return s.Stage != Idle && !active(s.Stage) })
}
func newTestController(p Processor, inj *fakeInjector, hide time.Duration) *Controller {
	return New(p, inj, hide)
}
func TestMockLifecycle(t *testing.T) {
	inj := &fakeInjector{}
	c := newTestController(Mock{Delay: 10 * time.Millisecond}, inj, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	first := waitState(t, c, func(s State) bool { return s.Visible })
	if first.Stage != Recording || !first.Visible || first.TaskID == "" || first.RawText != "" {
		t.Fatalf("bad start: %+v", first)
	}
	c.Toggle(Dialogue)
	if c.Snapshot().Stage != ASR {
		t.Fatal("missing ASR stage")
	}
	waitState(t, c, func(s State) bool { return s.Stage == LLM })
	end := terminalState(t, c)
	if end.Stage != Done || end.RawText == "" || end.Text == "" || end.HideAt == 0 || inj.calls() != 1 {
		t.Fatalf("bad end: %+v", end)
	}
	if end.TaskID != first.TaskID || end.Revision <= first.Revision {
		t.Fatal("task identity/revision lost")
	}
}
func TestMockBranchesNeverInject(t *testing.T) {
	for _, tt := range []struct {
		scenario string
		stage    Stage
	}{{"skip", Skip}, {"clarify", Clarify}, {"error", Error}} {
		t.Run(tt.scenario, func(t *testing.T) {
			inj := &fakeInjector{}
			c := newTestController(Mock{Scenario: tt.scenario}, inj, time.Hour)
			defer c.Close()
			c.Toggle(Dialogue)
			c.Toggle(Dialogue)
			end := terminalState(t, c)
			if end.Stage != tt.stage || end.Text != "" || inj.calls() != 0 {
				t.Fatalf("unexpected branch: %+v", end)
			}
			if end.RawText == "" {
				t.Fatal("lost ASR text")
			}
		})
	}
}
func TestInputFailureKeepsCopyableResult(t *testing.T) {
	inj := &fakeInjector{insertErr: errors.New("system input rejected")}
	c := newTestController(Mock{}, inj, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	end := terminalState(t, c)
	if end.Stage != Error || end.Text == "" || end.RawText == "" || inj.calls() != 0 {
		t.Fatalf("input fallback lost: %+v", end)
	}
}
func TestRepeatedHotkeysDoNotStartParallelTasks(t *testing.T) {
	inj := &fakeInjector{}
	c := newTestController(Mock{Delay: 30 * time.Millisecond}, inj, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	id := c.Snapshot().TaskID
	c.Toggle(Dialogue)
	var group sync.WaitGroup
	for range 30 {
		group.Go(func() { c.Toggle(Dialogue) })
	}
	group.Wait()
	if c.Snapshot().TaskID != id {
		t.Fatal("parallel task started")
	}
	if terminalState(t, c).Stage != Done || inj.calls() != 1 {
		t.Fatal("did not insert exactly once")
	}
}

type heldProcessor struct {
	entered  chan struct{}
	release  chan struct{}
	finished chan struct{}
	atLLM    bool
	once     sync.Once
}

func (p *heldProcessor) block() { p.once.Do(func() { close(p.entered) }); <-p.release }
func (p *heldProcessor) Transcribe(context.Context) (string, error) {
	if !p.atLLM {
		p.block()
		close(p.finished)
	}
	return "raw", nil
}
func (p *heldProcessor) Complete(context.Context, string) (Result, error) {
	if p.atLLM {
		p.block()
		close(p.finished)
	}
	return Result{Status: "insert", Text: "late result"}, nil
}
func TestCancellationRejectsLateProviderResults(t *testing.T) {
	for _, llm := range []bool{false, true} {
		t.Run(map[bool]string{false: "asr", true: "llm"}[llm], func(t *testing.T) {
			p := &heldProcessor{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}), atLLM: llm}
			inj := &fakeInjector{}
			c := newTestController(p, inj, time.Hour)
			defer c.Close()
			c.Toggle(Dialogue)
			oldID := c.Snapshot().TaskID
			c.Toggle(Dialogue)
			<-p.entered
			c.Close()
			if c.Snapshot().Stage != Cancelled || c.Snapshot().Visible {
				t.Fatal("not cancelled/hidden")
			}
			c.Toggle(Dialogue)
			fresh := c.Snapshot()
			if fresh.TaskID == oldID || fresh.RawText != "" || fresh.Text != "" {
				t.Fatal("new task reused old text")
			}
			close(p.release)
			<-p.finished
			// Wait until the obsolete worker has had time to attempt its guarded update.
			time.Sleep(20 * time.Millisecond)
			end := c.Snapshot()
			if end.TaskID != fresh.TaskID || end.Stage != Recording || !end.Visible || end.Text != "" || inj.calls() != 0 {
				t.Fatalf("late result escaped: %+v", end)
			}
		})
	}
}
func TestCloseWhileRecordingNeverProcesses(t *testing.T) {
	inj := &fakeInjector{}
	c := newTestController(Mock{}, inj, time.Hour)
	c.Toggle(Dialogue)
	c.Close()
	if c.Snapshot().Stage != Cancelled || c.Snapshot().Visible || inj.calls() != 0 {
		t.Fatal("recording close failed")
	}
}
func TestAutoHideAndOldTimerCannotHideNewTask(t *testing.T) {
	inj := &fakeInjector{}
	c := newTestController(Mock{}, inj, 60*time.Millisecond)
	defer c.Close()
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	terminalState(t, c)
	c.Toggle(Dialogue)
	id := c.Snapshot().TaskID
	time.Sleep(100 * time.Millisecond)
	if s := c.Snapshot(); s.TaskID != id || !s.Visible || s.Stage != Recording {
		t.Fatalf("old timer hid new task: %+v", s)
	}
	c.Toggle(Dialogue)
	terminalState(t, c)
	waitState(t, c, func(s State) bool { return !s.Visible })
	if c.Snapshot().Stage != Done {
		t.Fatal("auto hide destroyed terminal state")
	}
}

type invalidProcessor struct {
	result Result
	err    error
}

func (p invalidProcessor) Transcribe(context.Context) (string, error)       { return "original", nil }
func (p invalidProcessor) Complete(context.Context, string) (Result, error) { return p.result, p.err }
func TestInvalidResultNeverInjects(t *testing.T) {
	results := []Result{{Status: "unknown", Text: "no"}, {Status: "insert"}, {Status: "insert", Text: "   "}, {Status: "skip", Text: "no"}, {Status: "clarify", Text: "no", Notice: "q"}, {Status: "clarify"}}
	for _, result := range results {
		inj := &fakeInjector{}
		c := newTestController(invalidProcessor{result: result}, inj, time.Hour)
		c.Toggle(Dialogue)
		c.Toggle(Dialogue)
		end := terminalState(t, c)
		c.Close()
		if end.Stage != Error || end.Text != "" || inj.calls() != 0 {
			t.Fatalf("invalid output inserted: %+v", end)
		}
	}
}
func TestProviderErrorIsRedacted(t *testing.T) {
	inj := &fakeInjector{}
	c := newTestController(invalidProcessor{err: errors.New("sensitive provider body")}, inj, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	end := terminalState(t, c)
	if end.Stage != Error || end.Notice == "sensitive provider body" || end.RawText != "original" {
		t.Fatal("error leaked or lost ASR")
	}
}

type blockingInjector struct {
	entered   chan struct{}
	cancelled chan struct{}
}

func (i blockingInjector) Prepare(ctx context.Context, _ string) (func() error, error) {
	close(i.entered)
	<-ctx.Done()
	close(i.cancelled)
	return nil, ctx.Err()
}
func TestCloseCancelsDuringNativeValidation(t *testing.T) {
	inj := blockingInjector{entered: make(chan struct{}), cancelled: make(chan struct{})}
	c := New(Mock{}, inj, time.Hour)
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	<-inj.entered
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case <-inj.cancelled:
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked behind injection lock")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close did not return")
	}
	if c.Snapshot().Visible {
		t.Fatal("close did not hide")
	}
}

type latePreparation struct {
	entered      chan struct{}
	release      chan struct{}
	finished     chan struct{}
	commitCalled chan struct{}
}

func (i latePreparation) Prepare(context.Context, string) (func() error, error) {
	close(i.entered)
	<-i.release
	close(i.finished)
	// Deliberately ignores context; the controller must refuse this old commit.
	return func() error { close(i.commitCalled); return nil }, nil
}
func TestCancelledPreparationCannotCommitIntoNewTask(t *testing.T) {
	inj := latePreparation{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}), commitCalled: make(chan struct{})}
	c := New(Mock{}, inj, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	<-inj.entered
	c.Close()
	c.Toggle(Dialogue)
	id := c.Snapshot().TaskID
	close(inj.release)
	<-inj.finished
	select {
	case <-inj.commitCalled:
		t.Fatal("old preparation committed")
	case <-time.After(20 * time.Millisecond):
	}
	if s := c.Snapshot(); s.TaskID != id || s.Stage != Recording || s.Text != "" {
		t.Fatalf("old preparation changed new task: %+v", s)
	}
}
func TestCancelledContextBecomesSafeTerminal(t *testing.T) {
	p := &heldProcessor{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	inj := &fakeInjector{}
	c := New(p, inj, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	<-p.entered
	// Exercise the deadline/cancellation return path independently of UI Close.
	c.mu.Lock()
	c.cancel()
	c.mu.Unlock()
	close(p.release)
	end := terminalState(t, c)
	if end.Stage != Error || end.HideAt == 0 || inj.calls() != 0 {
		t.Fatalf("unsafe cancelled context: %+v", end)
	}
}

type startupProcessor struct {
	Mock
	entered chan struct{}
	release chan struct{}
	err     error
}

func (p startupProcessor) Begin(ctx context.Context) error {
	close(p.entered)
	select {
	case <-p.release:
		return p.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestStopDuringMicrophoneStartup(t *testing.T) {
	p := startupProcessor{entered: make(chan struct{}), release: make(chan struct{})}
	inj := &fakeInjector{}
	c := New(p, inj, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	<-p.entered
	c.Toggle(Dialogue)
	if c.Snapshot().Stage != ASR {
		t.Fatal("second press lost during startup")
	}
	close(p.release)
	if terminalState(t, c).Stage != Done || inj.calls() != 1 {
		t.Fatal("startup/stop ordering failed")
	}
}
func TestAutoStopAndWatchdog(t *testing.T) {
	inj := &fakeInjector{}
	c := NewWithFactory(func(context.Context, string, Intent, func(Stage, float64)) (Processor, TaskOptions, error) {
		return Mock{}, TaskOptions{Mode: "live", Hotkey: "Ctrl+Shift+R", RecordLimit: 15 * time.Millisecond, Timeout: time.Second}, nil
	}, inj, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	if s := waitState(t, c, func(s State) bool { return s.Hotkey == "Ctrl+Shift+R" }); s.Hotkey != "Ctrl+Shift+R" || s.Mode != "live" {
		t.Fatal("task config lost")
	}
	if terminalState(t, c).Stage != Done {
		t.Fatal("recording limit did not auto-stop")
	}
	p := &heldProcessor{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	timeout := NewWithFactory(func(context.Context, string, Intent, func(Stage, float64)) (Processor, TaskOptions, error) {
		return p, TaskOptions{Mode: "live", Timeout: 15 * time.Millisecond}, nil
	}, inj, time.Hour)
	defer timeout.Close()
	timeout.Toggle(Dialogue)
	timeout.Toggle(Dialogue)
	<-p.entered
	if terminalState(t, timeout).Stage != Error {
		t.Fatal("watchdog did not fire for uncooperative provider")
	}
	timeout.Toggle(Dialogue)
	id := timeout.Snapshot().TaskID
	close(p.release)
	<-p.finished
	time.Sleep(10 * time.Millisecond)
	if s := timeout.Snapshot(); s.TaskID != id || s.Stage != Recording {
		t.Fatal("late timeout result corrupted new task")
	}
}

func TestDeadlineIncludesPendingMicrophoneStartup(t *testing.T) {
	p := startupProcessor{entered: make(chan struct{}), release: make(chan struct{})}
	inj := &fakeInjector{}
	c := NewWithFactory(func(context.Context, string, Intent, func(Stage, float64)) (Processor, TaskOptions, error) {
		return p, TaskOptions{Mode: "live", Timeout: 15 * time.Millisecond}, nil
	}, inj, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	<-p.entered
	c.Toggle(Dialogue)
	end := terminalState(t, c)
	close(p.release)
	if end.Stage != Error || inj.calls() != 0 {
		t.Fatal("startup extended processing deadline or injected")
	}
}

func TestProcessingDoesNotRequireAnInputTarget(t *testing.T) {
	inj := &fakeInjector{}
	called := make(chan struct{}, 1)
	c := NewWithFactory(func(_ context.Context, _ string, intent Intent, _ func(Stage, float64)) (Processor, TaskOptions, error) {
		if intent != Dialogue {
			t.Error("unexpected task intent")
		}
		called <- struct{}{}
		return Mock{}, TaskOptions{}, nil
	}, inj, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	if s := c.Snapshot(); !s.Visible || s.Stage != Recording {
		t.Fatal("startup waited for a target")
	}
	c.Toggle(Dialogue)
	end := terminalState(t, c)
	select {
	case <-called:
	default:
		t.Fatal("factory not initialized")
	}
	if end.Stage != Done || end.Text == "" || inj.calls() != 1 {
		t.Fatalf("task did not submit: %+v", end)
	}
}

func TestCopyIsBoundToCurrentTask(t *testing.T) {
	inj := &fakeInjector{}
	c := New(Mock{}, inj, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	id := c.Snapshot().TaskID
	if _, err := c.CopyText(id); err == nil {
		t.Fatal("claimed a copy with no text")
	}
	c.Toggle(Dialogue)
	end := terminalState(t, c)
	if text, err := c.CopyText(id); err != nil || text != end.Text {
		t.Fatal("current result not copyable")
	}
	c.Toggle(Dialogue)
	if _, err := c.CopyText(id); err == nil {
		t.Fatal("stale click copied another task")
	}
}
