package hotkey

import (
	"voice_input/internal/i18n"
)

type Registry interface {
	Register(string, func()) error
	Unregister(string) error
	GetAll() []string
}

// Replace runs on the UI thread. A physical shortcut retained in the new set
// stays registered, so exchanging the two purposes does not cause conflicts.
// saved is true even if releasing an obsolete binding fails after saving.
func Replace(reg Registry, desired []string, fired func(Combination), save func() error) (saved bool, err error) {
	wanted := map[Combination]bool{}
	for _, raw := range desired {
		key, err := Parse(raw)
		if err != nil {
			return false, err
		}
		if wanted[key] {
			return false, i18n.Error("hotkey.same")
		}
		wanted[key] = true
	}
	old := map[Combination]bool{}
	for _, raw := range reg.GetAll() {
		key, err := Parse(raw)
		if err != nil {
			return false, i18n.Error("hotkey.old_invalid")
		}
		old[key] = true
	}
	added := []Combination{}
	rollback := func(cause error) (bool, error) {
		failed := false
		for _, key := range added {
			if reg.Unregister(key.Accelerator()) != nil {
				failed = true
			}
		}
		if failed {
			return false, i18n.Error("hotkey.rollback")
		}
		return false, cause
	}
	for _, raw := range desired {
		key, _ := Parse(raw)
		if old[key] {
			continue
		}
		if err := reg.Register(key.Accelerator(), func() { fired(key) }); err != nil {
			return rollback(i18n.Wrap(err, "hotkey.register", "hotkey", raw))
		}
		added = append(added, key)
	}
	if err := save(); err != nil {
		return rollback(err)
	}
	failed := false
	for key := range old {
		if !wanted[key] && reg.Unregister(key.Accelerator()) != nil {
			failed = true
		}
	}
	if failed {
		return true, i18n.Error("hotkey.release")
	}
	return true, nil
}
