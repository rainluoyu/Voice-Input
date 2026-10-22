// Package startup manages the current user's Windows login registration.
package startup

import (
	"bytes"
	"errors"
	"sync"

	"voice_input/internal/i18n"
)

// Options are independent preferences: Admin has no effect while Enabled is false.
type Options struct {
	Enabled bool
	Admin   bool
}

var saveMu sync.Mutex

// Save changes registration before calling persist. A failed persist restores the
// actual registration snapshot, not a reconstruction from previous preferences.
// An unchanged preference does not repair or take ownership of external changes.
// persist must not call Save, and must itself leave settings unchanged on failure.
func Save(previous, next Options, persist func() error) error {
	saveMu.Lock()
	defer saveMu.Unlock()
	if previous == next || (!previous.Enabled && !next.Enabled) {
		return persist()
	}
	b, normal, err := nativeBackend()
	if err != nil {
		return startupError(err)
	}
	return save(b, normal, next, persist)
}

// runValue deliberately preserves arbitrary registry types and bytes, including
// malformed strings and present-but-empty values. It is never helper input.
type runValue struct {
	exists bool
	kind   uint32
	data   []byte
}

func (v runValue) equal(other runValue) bool {
	return v.exists == other.exists && (!v.exists || v.kind == other.kind && bytes.Equal(v.data, other.data))
}

type backend interface {
	readRun() (runValue, error)
	// replaceRun refuses to overwrite a value changed since the snapshot.
	replaceRun(previous, next runValue) error
	// A task is either absent or exactly our fixed definition and ACL. Anything
	// else is an error: restoring arbitrary task XML is intentionally unsupported.
	readTask() (bool, error)
	setTask(enabled bool) error
}

func save(b backend, normal runValue, next Options, persist func() error) error {
	oldRun, err := b.readRun()
	if err != nil {
		return startupError(err)
	}
	oldTask, err := b.readTask()
	if err != nil {
		return startupError(err)
	}
	newRun := oldRun
	if next.Enabled && !next.Admin {
		newRun = normal
	} else if oldRun.equal(normal) {
		newRun = runValue{}
	}
	newTask := next.Enabled && next.Admin
	taskAttempted, runAttempted := false, false
	rollback := func(cause error) error {
		var failures []error
		if taskAttempted {
			if err := b.setTask(oldTask); err != nil {
				failures = append(failures, err)
			}
		}
		if runAttempted {
			current, err := b.readRun()
			if err == nil && !current.equal(oldRun) {
				if !current.equal(newRun) {
					err = errors.New("startup Run value changed during rollback")
				} else {
					err = b.replaceRun(current, oldRun)
				}
			}
			if err != nil {
				failures = append(failures, err)
			}
		}
		if len(failures) != 0 {
			return i18n.Wrap(errors.Join(append([]error{cause}, failures...)...), "startup.rollback_failed")
		}
		return cause
	}
	// Obtain consent before changing HKCU, so a cancelled UAC normally needs no
	// registry compensation. Both operations may still fail after taking effect.
	if newTask != oldTask {
		taskAttempted = true
		if err := b.setTask(newTask); err != nil {
			return rollback(startupError(err))
		}
	}
	if !newRun.equal(oldRun) {
		runAttempted = true
		if err := b.replaceRun(oldRun, newRun); err != nil {
			return rollback(startupError(err))
		}
	}
	if err := persist(); err != nil {
		return rollback(err)
	}
	return nil
}

func startupError(err error) error {
	var descriptor i18n.Descriptor
	if errors.As(err, &descriptor) {
		switch descriptor.I18nMessage().Code {
		case "startup.failed", "startup.cancelled", "startup.identity_failed", "startup.rollback_failed":
			return err
		}
	}
	return i18n.Wrap(err, "startup.failed")
}

const (
	helperFlag   = "--voice-input-startup-helper"
	ensureAdmin  = "ensure-admin"
	removeAdmin  = "remove-admin"
	exitFailed   = 1
	exitIdentity = 2
)

// parseHelper never accepts executable paths, task names, commands or XML. A
// malformed invocation beginning with our reserved flag is handled as a failure,
// rather than falling through into the normal application.
func parseHelper(args []string) (handled bool, action, sid string, valid bool) {
	if len(args) == 0 || args[0] != helperFlag {
		return false, "", "", false
	}
	if len(args) != 3 || (args[1] != ensureAdmin && args[1] != removeAdmin) {
		return true, "", "", false
	}
	return true, args[1], args[2], true
}
