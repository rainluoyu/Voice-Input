//go:build windows

package startup

import (
	"os"
	"testing"
)

// This opt-in test only reads the current user's existing task. It never calls
// registration, deletion, the elevation helper or configuration persistence.
func TestInspectExistingStartupTaskReadOnly(t *testing.T) {
	exe := os.Getenv("VOICE_INPUT_STARTUP_READONLY_EXE")
	if exe == "" {
		t.Skip("set VOICE_INPUT_STARTUP_READONLY_EXE to inspect an existing task")
	}
	sid, _, err := identity()
	if err != nil {
		t.Fatal(err)
	}
	b := windowsBackend{exe: exe, sid: sid}
	present, err := b.readTask()
	if err != nil {
		t.Fatal("existing startup task was rejected:", err)
	}
	if !present {
		t.Fatal("no existing startup task was found for the current user")
	}
}
