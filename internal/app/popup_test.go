package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMouseInteractionKeepsPopupUntilClosed(t *testing.T) {
	for _, duringRecording := range []bool{false, true} {
		t.Run(map[bool]string{false: "after_completion", true: "during_recording"}[duringRecording], func(t *testing.T) {
			c := New(Mock{}, &fakeInjector{}, 40*time.Millisecond)
			defer c.Close()
			c.Toggle(Dialogue)
			id := c.Snapshot().TaskID
			if duringRecording {
				c.KeepOpen(id)
			}
			c.Toggle(Dialogue)
			end := terminalState(t, c)
			if !duringRecording {
				if end.HideAt == 0 {
					t.Fatal("uninteracted result has no countdown")
				}
				c.KeepOpen(id)
			}
			time.Sleep(90 * time.Millisecond)
			if s := c.Snapshot(); !s.Visible || !s.Pinned || s.HideAt != 0 {
				t.Fatalf("pinned task expired: %+v", s)
			}
			if text, err := c.CopyText(id); err != nil || text != end.Text {
				t.Fatal("pinned text is not copyable")
			}
			c.Close()
			if s := c.Snapshot(); s.Visible || s.Pinned || s.HideAt != 0 {
				t.Fatalf("manual close failed: %+v", s)
			}
		})
	}
}

func TestOldInteractionCannotPinNewOrClosedTask(t *testing.T) {
	c := New(Mock{}, &fakeInjector{}, 40*time.Millisecond)
	defer c.Close()
	c.Toggle(Dialogue)
	old := c.Snapshot().TaskID
	c.KeepOpen(old)
	c.Toggle(Dialogue)
	terminalState(t, c)
	c.Toggle(Dialogue)
	current := c.Snapshot().TaskID
	c.KeepOpen(old)
	c.KeepOpen("")
	if s := c.Snapshot(); s.Pinned || current == old {
		t.Fatalf("new task retained pin: %+v", s)
	}
	c.Toggle(Dialogue)
	terminalState(t, c)
	waitState(t, c, func(s State) bool { return !s.Visible })
	c.KeepOpen(current)
	if s := c.Snapshot(); s.Visible || s.Pinned {
		t.Fatalf("late interaction reopened hidden task: %+v", s)
	}
}

func TestPinWhilePreparingStillAllowsCancellation(t *testing.T) {
	inj := blockingInjector{entered: make(chan struct{}), cancelled: make(chan struct{})}
	c := New(Mock{}, inj, time.Hour)
	c.Toggle(Dialogue)
	id := c.Snapshot().TaskID
	c.Toggle(Dialogue)
	<-inj.entered
	c.KeepOpen(id)
	c.Close()
	select {
	case <-inj.cancelled:
	case <-time.After(time.Second):
		t.Fatal("pinned task did not cancel")
	}
	if s := c.Snapshot(); s.Visible || s.Pinned || s.Stage != Cancelled {
		t.Fatal(s)
	}
}

func TestSnapshotDoesNotExposeTimingOrTargetStatistics(t *testing.T) {
	c := New(Mock{}, &fakeInjector{}, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	terminalState(t, c)
	data, err := json.Marshal(c.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"elapsed_ms", "timings_ms", "target_app"} {
		if strings.Contains(string(data), field) {
			t.Fatalf("removed field still exposed: %s", field)
		}
	}
}

type currentPositionInjector struct {
	mu                  sync.Mutex
	position, submitted string
}

func (i *currentPositionInjector) Prepare(ctx context.Context, _ string) (func() error, error) {
	return func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		i.mu.Lock()
		defer i.mu.Unlock()
		i.submitted = i.position
		return nil
	}, nil
}
func TestResultGoesToCurrentPositionNotStartPosition(t *testing.T) {
	p := &heldProcessor{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}), atLLM: true}
	inj := &currentPositionInjector{position: "first"}
	c := New(p, inj, time.Hour)
	defer c.Close()
	c.Toggle(Dialogue)
	c.Toggle(Dialogue)
	<-p.entered
	inj.mu.Lock()
	inj.position = "second"
	inj.mu.Unlock()
	close(p.release)
	if end := terminalState(t, c); end.Stage != Done {
		t.Fatal(end)
	}
	inj.mu.Lock()
	defer inj.mu.Unlock()
	if inj.submitted != "second" {
		t.Fatal("result did not follow current position")
	}
}
