package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGeneralSettingsCompatibilityAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"version":1}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()
	if cfg.General != Default().General || cfg.Popup.AutoHideSeconds != 30 {
		t.Fatalf("legacy defaults changed: %+v", cfg)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("opening legacy settings rewrote the file")
	}
	cfg.General = General{Proxy: cfg.General.Proxy, StartWithWindows: true, StartAsAdmin: true, AutoCopy: true, MuteWhileRecording: true}
	cfg.Popup.AutoHideSeconds = 75
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Open(path)
	if err != nil || reloaded.Get() != cfg {
		t.Fatalf("general settings did not round trip: %v", err)
	}
	cfg.General = Default().General
	cfg.General.Proxy.Enabled = false
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	reloaded, err = Open(path)
	if err != nil || reloaded.Get().General != cfg.General {
		t.Fatalf("explicit false settings changed: %v", err)
	}
}

func TestGeneralSettingsValidation(t *testing.T) {
	for _, seconds := range []int{1, 30, 3600} {
		cfg := Default()
		cfg.Popup.AutoHideSeconds = seconds
		if err := cfg.Validate(); err != nil {
			t.Fatalf("valid wait %d rejected: %v", seconds, err)
		}
	}
	for _, seconds := range []int{-1, 0, 3601} {
		cfg := Default()
		cfg.Popup.AutoHideSeconds = seconds
		if cfg.Validate() == nil {
			t.Fatalf("invalid wait %d accepted", seconds)
		}
	}
	cfg := Default()
	cfg.General.StartAsAdmin = true
	if cfg.Validate() == nil {
		t.Fatal("administrator startup accepted without startup")
	}
}
