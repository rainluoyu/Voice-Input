package startup

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"voice_input/internal/i18n"
)

var errInjected = errors.New("injected failure")

type fakeBackend struct {
	run         runValue
	task        bool
	foreignTask bool
	readRunErr  error
	readTaskErr error
	runBefore   map[int]error
	runAfter    map[int]error
	taskBefore  map[int]error
	taskAfter   map[int]error
	runWrites   int
	uac         int
	changes     []string
}

func (b *fakeBackend) readRun() (runValue, error) {
	v := b.run
	v.data = append([]byte(nil), v.data...)
	return v, b.readRunErr
}

func (b *fakeBackend) replaceRun(previous, next runValue) error {
	if !b.run.equal(previous) {
		return errors.New("Run snapshot conflict")
	}
	b.runWrites++
	if err := b.runBefore[b.runWrites]; err != nil {
		return err
	}
	b.run = next
	b.run.data = append([]byte(nil), next.data...)
	b.changes = append(b.changes, "run")
	return b.runAfter[b.runWrites]
}

func (b *fakeBackend) readTask() (bool, error) {
	if b.foreignTask {
		return false, errors.New("task modified externally")
	}
	return b.task, b.readTaskErr
}

func (b *fakeBackend) setTask(enabled bool) error {
	present, err := b.readTask()
	if err != nil || present == enabled {
		return err
	}
	b.uac++
	if err := b.taskBefore[b.uac]; err != nil {
		return err
	}
	b.task = enabled
	b.changes = append(b.changes, fmt.Sprint("task=", enabled))
	return b.taskAfter[b.uac]
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var descriptor i18n.Descriptor
	if !errors.As(err, &descriptor) {
		t.Fatalf("error has no i18n descriptor: %v", err)
	}
	return descriptor.I18nMessage().Code
}

func TestSaveRegistrationModes(t *testing.T) {
	normal := normalRun(`C:\Program Files\voice_input.exe`)
	foreign := runValue{exists: true, kind: 3, data: []byte{1, 0, 2}}
	tests := []struct {
		name     string
		run      runValue
		task     bool
		next     Options
		wantRun  runValue
		wantTask bool
		wantUAC  int
		writes   int
	}{
		{"enable normal", runValue{}, false, Options{Enabled: true}, normal, false, 0, 1},
		{"enable admin", runValue{}, false, Options{Enabled: true, Admin: true}, runValue{}, true, 1, 0},
		{"normal to admin", normal, false, Options{Enabled: true, Admin: true}, runValue{}, true, 1, 1},
		{"admin to normal", runValue{}, true, Options{Enabled: true}, normal, false, 1, 1},
		{"disable normal", normal, false, Options{}, runValue{}, false, 0, 1},
		{"disable admin", runValue{}, true, Options{}, runValue{}, false, 1, 0},
		{"disabled admin preference", runValue{}, false, Options{Admin: true}, runValue{}, false, 0, 0},
		{"disabled foreign Run", foreign, false, Options{}, foreign, false, 0, 0},
		{"normal already exact", normal, false, Options{Enabled: true}, normal, false, 0, 0},
		{"admin already exact", runValue{}, true, Options{Enabled: true, Admin: true}, runValue{}, true, 0, 0},
		{"enable normal overwrites fixed value", foreign, false, Options{Enabled: true}, normal, false, 0, 1},
		{"admin preserves foreign Run", foreign, false, Options{Enabled: true, Admin: true}, foreign, true, 1, 0},
		{"remove duplicate registrations", normal, true, Options{Enabled: true}, normal, false, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &fakeBackend{run: tt.run, task: tt.task}
			persisted := 0
			err := save(b, normal, tt.next, func() error {
				persisted++
				if !b.run.equal(tt.wantRun) || b.task != tt.wantTask {
					t.Fatal("persist called before applying registration")
				}
				return nil
			})
			if err != nil || persisted != 1 {
				t.Fatalf("save = %v; persist calls = %d", err, persisted)
			}
			if !b.run.equal(tt.wantRun) || b.task != tt.wantTask || b.uac != tt.wantUAC || b.runWrites != tt.writes {
				t.Fatalf("state = %#v; want run=%#v task=%v UAC=%d writes=%d", b, tt.wantRun, tt.wantTask, tt.wantUAC, tt.writes)
			}
		})
	}
}

func TestPersistenceFailureRestoresRawSnapshots(t *testing.T) {
	normal := normalRun(`C:\voice_input.exe`)
	values := []runValue{
		{},
		{exists: true, kind: 1}, // Present-but-empty REG_SZ, not absent.
		{exists: true, kind: 1, data: []byte{0x41}}, // Malformed UTF-16.
		{exists: true, kind: 2, data: []byte{0x25, 0, 0, 0}},
		{exists: true, kind: 3, data: []byte{0, 255, 0, 128}},
		{exists: true, kind: 4, data: []byte{1, 2, 3, 4}},
		normal,
	}
	modes := []Options{{}, {Enabled: true}, {Enabled: true, Admin: true}}
	for i, value := range values {
		for _, oldTask := range []bool{false, true} {
			for j, next := range modes {
				t.Run(fmt.Sprintf("raw%d-task%v-mode%d", i, oldTask, j), func(t *testing.T) {
					b := &fakeBackend{run: value, task: oldTask}
					err := save(b, normal, next, func() error { return errInjected })
					if err != errInjected {
						t.Fatalf("persist error was not preserved: %v", err)
					}
					if !b.run.equal(value) || b.task != oldTask {
						t.Fatalf("snapshot not restored: %#v", b)
					}
				})
			}
		}
	}
}

func TestRegistrationFailureDoesNotPersist(t *testing.T) {
	normal := normalRun(`C:\voice_input.exe`)
	tests := []struct {
		name string
		b    fakeBackend
		next Options
		code string
	}{
		{"read Run", fakeBackend{readRunErr: errInjected}, Options{Enabled: true}, "startup.failed"},
		{"read task", fakeBackend{readTaskErr: errInjected}, Options{Enabled: true}, "startup.failed"},
		{"foreign task", fakeBackend{foreignTask: true}, Options{Enabled: true}, "startup.failed"},
		{"Run write before", fakeBackend{runBefore: map[int]error{1: errInjected}}, Options{Enabled: true}, "startup.failed"},
		{"Run write after", fakeBackend{runAfter: map[int]error{1: errInjected}}, Options{Enabled: true}, "startup.failed"},
		{"task write before", fakeBackend{taskBefore: map[int]error{1: errInjected}}, Options{Enabled: true, Admin: true}, "startup.failed"},
		{"task write after", fakeBackend{taskAfter: map[int]error{1: errInjected}}, Options{Enabled: true, Admin: true}, "startup.failed"},
		{"task removal after", fakeBackend{task: true, taskAfter: map[int]error{1: errInjected}}, Options{Enabled: true}, "startup.failed"},
		{"cancelled", fakeBackend{run: normal, taskBefore: map[int]error{1: i18n.Error("startup.cancelled")}}, Options{Enabled: true, Admin: true}, "startup.cancelled"},
		{"different account", fakeBackend{taskBefore: map[int]error{1: i18n.Error("startup.identity_failed")}}, Options{Enabled: true, Admin: true}, "startup.identity_failed"},
		{"Run failure after removing task", fakeBackend{task: true, runBefore: map[int]error{1: errInjected}}, Options{Enabled: true}, "startup.failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldRun, oldTask := tt.b.run, tt.b.task
			persisted := false
			err := save(&tt.b, normal, tt.next, func() error { persisted = true; return nil })
			if persisted || codeOf(t, err) != tt.code {
				t.Fatalf("persisted=%v, code=%s", persisted, codeOf(t, err))
			}
			if !tt.b.run.equal(oldRun) || tt.b.task != oldTask {
				t.Fatalf("registration failure did not compensate: %#v", tt.b)
			}
			if tt.code == "startup.cancelled" && (tt.b.runWrites != 0 || tt.b.uac != 1) {
				t.Fatal("cancellation should not change Run or request another UAC")
			}
		})
	}
}

func TestRollbackFailuresAreExplicitAndKeepCause(t *testing.T) {
	normal := normalRun(`C:\voice_input.exe`)
	tests := []struct {
		name string
		b    fakeBackend
		next Options
	}{
		{"Run restore", fakeBackend{runBefore: map[int]error{2: errors.New("restore denied")}}, Options{Enabled: true}},
		{"task restore", fakeBackend{taskBefore: map[int]error{2: i18n.Error("startup.cancelled")}}, Options{Enabled: true, Admin: true}},
		{"restore both despite task failure", fakeBackend{run: normal, taskBefore: map[int]error{2: errInjected}}, Options{Enabled: true, Admin: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldRun := tt.b.run
			err := save(&tt.b, normal, tt.next, func() error { return errInjected })
			if codeOf(t, err) != "startup.rollback_failed" || !errors.Is(err, errInjected) {
				t.Fatalf("wrong rollback error: %#v", err)
			}
			if tt.name == "restore both despite task failure" && !tt.b.run.equal(oldRun) {
				t.Fatal("Run restoration was skipped after task restoration failed")
			}
		})
	}
}

func TestRollbackRefusesExternalChanges(t *testing.T) {
	normal := normalRun(`C:\voice_input.exe`)
	t.Run("Run", func(t *testing.T) {
		external := runValue{exists: true, kind: 3, data: []byte{9, 8, 7}}
		b := &fakeBackend{}
		err := save(b, normal, Options{Enabled: true}, func() error {
			b.run = external
			return errInjected
		})
		if codeOf(t, err) != "startup.rollback_failed" || !b.run.equal(external) || b.runWrites != 1 {
			t.Fatalf("external Run was overwritten: %#v", b)
		}
	})
	t.Run("task", func(t *testing.T) {
		b := &fakeBackend{run: normal}
		err := save(b, normal, Options{Enabled: true, Admin: true}, func() error {
			b.foreignTask = true
			return errInjected
		})
		if codeOf(t, err) != "startup.rollback_failed" || !b.foreignTask || !b.run.equal(normal) || b.uac != 1 {
			t.Fatalf("external task was overwritten or Run not restored: %#v", b)
		}
	})
}

func TestSaveUnchangedPreferencesOnlyPersists(t *testing.T) {
	for _, opts := range []Options{{}, {Admin: true}, {Enabled: true}, {Enabled: true, Admin: true}} {
		calls := 0
		err := Save(opts, opts, func() error { calls++; return errInjected })
		if err != errInjected || calls != 1 {
			t.Fatalf("unchanged options accessed backend or lost persistence error: %v", err)
		}
	}
	if err := Save(Options{}, Options{Admin: true}, func() error { return nil }); err != nil {
		t.Fatalf("disabled preference change accessed backend: %v", err)
	}
}

func TestTaskChangePrecedesRunAndPersistence(t *testing.T) {
	normal := normalRun(`C:\voice_input.exe`)
	b := &fakeBackend{run: normal}
	err := save(b, normal, Options{Enabled: true, Admin: true}, func() error {
		b.changes = append(b.changes, "persist")
		return errInjected
	})
	want := []string{"task=true", "run", "persist", "task=false", "run"}
	if err != errInjected || !reflect.DeepEqual(b.changes, want) {
		t.Fatalf("operation order %v, want %v; err=%v", b.changes, want, err)
	}
}

func TestParseHelperRejectsUnboundedInputs(t *testing.T) {
	for _, args := range [][]string{
		{helperFlag},
		{helperFlag, "arbitrary-command", "S-1-5-21-1"},
		{helperFlag, ensureAdmin},
		{helperFlag, ensureAdmin, "S-1-5-21-1", `C:\evil.xml`},
		{helperFlag, removeAdmin, "S-1-5-21-1", `C:\evil.exe`},
	} {
		handled, _, _, valid := parseHelper(args)
		if !handled || valid {
			t.Fatalf("accepted malformed helper invocation: %v", args)
		}
		// Invalid arity/action must not consult any Windows APIs either.
		if handled, code := HandleHelper(args); !handled || code == 0 {
			t.Fatalf("malformed helper fell through: %v", args)
		}
	}
	for _, action := range []string{ensureAdmin, removeAdmin} {
		handled, gotAction, sid, valid := parseHelper([]string{helperFlag, action, "S-1-5-21-1"})
		if !handled || !valid || gotAction != action || sid != "S-1-5-21-1" {
			t.Fatal("fixed helper invocation rejected")
		}
	}
	for _, args := range [][]string{nil, {"--other"}, {"ordinary", helperFlag}} {
		if handled, _, _, _ := parseHelper(args); handled {
			t.Fatalf("ordinary args treated as helper: %v", args)
		}
	}
}
