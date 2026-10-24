package tray

import (
	"reflect"
	"testing"
)

type fakeShell struct {
	present     bool
	addFailures int
	legacy      bool
	calls       []operation
}

func (s *fakeShell) notify(op operation) bool {
	s.calls = append(s.calls, op)
	switch op {
	case modify:
		return s.present
	case add:
		if s.addFailures > 0 {
			s.addFailures--
			return false
		}
		if s.present {
			panic("duplicate icon addition")
		}
		s.present = true
		return true
	case setVersion:
		return !s.legacy
	case remove:
		s.present = false
		return true
	}
	panic("unknown shell operation")
}

func TestRegistrationRetriesUntilShellIsReady(t *testing.T) {
	shell := &fakeShell{addFailures: 2}
	r := registration{notify: shell.notify}
	for i := 0; i < 2; i++ {
		if got := r.restore(); got != retry || r.added || r.failures != i+1 {
			t.Fatalf("failed add should retry: %v %+v", got, r)
		}
	}
	if got := r.restore(); got != ready || !r.added || r.failures != 0 {
		t.Fatalf("successful add should stop retries: %v %+v", got, r)
	}
	want := []operation{modify, add, modify, add, modify, add, setVersion}
	if !reflect.DeepEqual(shell.calls, want) {
		t.Fatal("unexpected registration sequence", shell.calls)
	}
}

func TestRegistrationRecoversAfterExplorerRestart(t *testing.T) {
	shell := &fakeShell{}
	r := registration{notify: shell.notify}
	if r.restore() != ready {
		t.Fatal("initial registration failed")
	}
	for i := 0; i < 3; i++ {
		shell.calls = nil
		if r.restore() != ready || !reflect.DeepEqual(shell.calls, []operation{modify}) {
			t.Fatal("duplicate broadcast must only modify the live icon", shell.calls)
		}
	}
	shell.present, shell.addFailures = false, 1
	if r.restore() != retry || r.added {
		t.Fatal("shell restart failure did not retry")
	}
	if r.restore() != ready || !r.added || !shell.present {
		t.Fatal("shell restart did not recover")
	}
}

func TestExistingIconDoesNotNeedCachedCallbackVersion(t *testing.T) {
	shell := &fakeShell{present: true}
	r := registration{notify: shell.notify}
	if r.restore() != ready || !reflect.DeepEqual(shell.calls, []operation{modify}) {
		t.Fatal("existing icon should be modified, not added", shell.calls)
	}
	for _, payload := range [][2]uintptr{{1, leftButtonUp}, {123, 1<<16 | leftButtonUp}} {
		if notificationFor(1, payload[0], payload[1]) != activate {
			t.Fatal("existing icon notification depended on a cached version", payload)
		}
	}
}

func TestSetVersionFailureDoesNotRepeatAdd(t *testing.T) {
	shell := &fakeShell{legacy: true}
	r := registration{notify: shell.notify}
	if r.restore() != ready || !r.added {
		t.Fatal("SETVERSION failure must keep the legacy icon")
	}
	shell.calls = nil
	if r.restore() != ready || !reflect.DeepEqual(shell.calls, []operation{modify}) {
		t.Fatal("valid legacy icon was added twice", shell.calls)
	}
	// A new Explorer can support v4 even if the previous registration did not.
	shell.present, shell.legacy, shell.calls = false, false, nil
	if r.restore() != ready || !reflect.DeepEqual(shell.calls, []operation{modify, add, setVersion}) {
		t.Fatal("version negotiation was not repeated for the new icon", shell.calls)
	}
}

func TestRegistrationHasBoundedRetries(t *testing.T) {
	shell := &fakeShell{addFailures: retryLimit}
	r := registration{notify: shell.notify}
	for i := 1; i <= retryLimit; i++ {
		want := retry
		if i == retryLimit {
			want = unavailable
		}
		if got := r.restore(); got != want {
			t.Fatalf("attempt %d: got %v, want %v", i, got, want)
		}
	}
	if r.added || shell.present || r.failures != retryLimit {
		t.Fatal("failed registration reported a live icon")
	}
	// A later TaskbarCreated starts a new bounded recovery cycle.
	r.failures = 0
	if r.restore() != ready || r.failures != 0 {
		t.Fatal("a later Explorer startup could not recover")
	}
}

func TestCloseCancelsRecoveryAndDeletesOnlyOnce(t *testing.T) {
	for _, pending := range []bool{false, true} {
		shell := &fakeShell{}
		if pending {
			shell.addFailures = 1
		}
		r := registration{notify: shell.notify}
		r.restore()
		shell.calls = nil
		r.close()
		r.close()
		if r.restore() != stopped || r.added || shell.present {
			t.Fatal("closed tray recovered or retained its icon", pending)
		}
		if !reflect.DeepEqual(shell.calls, []operation{remove}) {
			t.Fatal("close did not release exactly once", pending, shell.calls)
		}
	}
	var unstarted registration
	unstarted.close()
	if unstarted.restore() != stopped {
		t.Fatal("unstarted closed tray attempted registration")
	}
}
