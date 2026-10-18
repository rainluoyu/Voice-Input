package hotkey

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"voice_input/internal/task"
)

func TestWindowsCombinationsAndDispatch(t *testing.T) {
	for _, pair := range [][2]string{{"Ctrl+Alt+R", " alt + CONTROL + r "}, {"Win+Shift+F2", "Super+Shift+f2"}, {"CmdOrCtrl+Return", "Ctrl+Enter"}, {"Ctrl+Plus", "Ctrl+="}} {
		a, err := Parse(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		b, err := Parse(pair[1])
		if err != nil || a != b {
			t.Fatal(pair, a, b, err)
		}
		c, err := Parse(a.Accelerator())
		if err != nil || c != a {
			t.Fatal("canonical round trip", a, c, err)
		}
		if FromMessage(uintptr(a.Key)<<16|uintptr(a.Modifiers)|0x4000) != a {
			t.Fatal("native decoding failed")
		}
	}
	for _, raw := range []string{"", "Ctrl+", "Ctrl++", "Unknown+R", "Ctrl+F25", "Ctrl+不存在"} {
		if _, err := Parse(raw); err == nil {
			t.Fatal("invalid key accepted", raw)
		}
	}
	for _, tt := range []struct {
		raw    string
		intent task.Intent
		ok     bool
	}{{"Ctrl+Alt+R", task.Dictation, true}, {"Ctrl+Alt+A", task.Dialogue, true}, {"Ctrl+Alt+Q", "", false}} {
		key, _ := Parse(tt.raw)
		intent, ok := Resolve(key, "Ctrl+Alt+R", "Ctrl+Alt+A")
		if intent != tt.intent || ok != tt.ok {
			t.Fatal(tt, intent, ok)
		}
	}
}

type fakeRegistry struct {
	keys                         map[string]func()
	failRegister, failUnregister string
	registered, removed          []string
}

func registry(keys ...string) *fakeRegistry {
	r := &fakeRegistry{keys: map[string]func(){}}
	for _, raw := range keys {
		key, _ := Parse(raw)
		r.keys[key.Accelerator()] = func() {}
	}
	return r
}
func (r *fakeRegistry) GetAll() []string {
	keys := []string{}
	for key := range r.keys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func (r *fakeRegistry) Register(key string, callback func()) error {
	r.registered = append(r.registered, key)
	if key == r.failRegister || r.keys[key] != nil {
		return errors.New("occupied")
	}
	r.keys[key] = callback
	return nil
}
func (r *fakeRegistry) Unregister(key string) error {
	r.removed = append(r.removed, key)
	if key == r.failUnregister {
		return errors.New("cannot unregister")
	}
	delete(r.keys, key)
	return nil
}
func TestShortcutSetUpdatesAndSwaps(t *testing.T) {
	r := registry("Ctrl+Alt+R", "Ctrl+Alt+A")
	before := r.GetAll()
	saved, err := Replace(r, []string{"Alt+Ctrl+A", "Ctrl+Alt+R"}, func(Combination) {}, func() error { return nil })
	if err != nil || !saved || len(r.registered) != 0 || len(r.removed) != 0 || !reflect.DeepEqual(before, r.GetAll()) {
		t.Fatal("swap rebinding", err)
	}
	var fired Combination
	saved, err = Replace(r, []string{"Ctrl+Shift+R", "Ctrl+Shift+A"}, func(c Combination) { fired = c }, func() error { return nil })
	if err != nil || !saved || len(r.keys) != 2 {
		t.Fatal(err)
	}
	r.keys["Ctrl+Shift+A"]()
	key, _ := Parse("Ctrl+Shift+A")
	if fired != key {
		t.Fatal("callback has wrong physical shortcut")
	}
}
func TestFailedUpdatesPreserveOldBindings(t *testing.T) {
	for _, failure := range []string{"second_register", "save", "duplicate"} {
		t.Run(failure, func(t *testing.T) {
			r := registry("Ctrl+Alt+R", "Ctrl+Alt+A")
			before := r.GetAll()
			wanted := []string{"Ctrl+Shift+R", "Ctrl+Shift+A"}
			if failure == "second_register" {
				r.failRegister = "Ctrl+Shift+A"
			}
			if failure == "duplicate" {
				wanted[1] = "Shift+Ctrl+r"
			}
			saves := 0
			saved, err := Replace(r, wanted, func(Combination) {}, func() error { saves++; return errors.New("storage failed") })
			if err == nil || saved || !reflect.DeepEqual(before, r.GetAll()) {
				t.Fatal("old bindings lost", saved, err, r.GetAll())
			}
			if failure != "save" && saves != 0 {
				t.Fatal("saved before validation/registration succeeded")
			}
		})
	}
}
func TestRollbackAndUnregisterFailuresAreReported(t *testing.T) {
	r := registry("Ctrl+Alt+R", "Ctrl+Alt+A")
	r.failRegister, r.failUnregister = "Ctrl+Shift+A", "Ctrl+Shift+R"
	if saved, err := Replace(r, []string{"Ctrl+Shift+R", "Ctrl+Shift+A"}, func(Combination) {}, func() error { t.Fatal("unexpected save"); return nil }); saved || err == nil {
		t.Fatal("rollback failure concealed")
	}
	r = registry("Ctrl+Alt+R", "Ctrl+Alt+A")
	r.failUnregister = "Ctrl+Alt+R"
	if saved, err := Replace(r, []string{"Ctrl+Shift+R", "Ctrl+Shift+A"}, func(Combination) {}, func() error { return nil }); !saved || err == nil {
		t.Fatal("post-save unbind failure concealed")
	}
}
