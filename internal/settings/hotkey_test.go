package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"voice_input/internal/hotkey"
	"voice_input/internal/task"
)

func TestLegacyShortcutMigrationDoesNotRewrite(t *testing.T) {
	for _, old := range []string{"Ctrl+Alt+R", "alt+ctrl+a", "Ctrl+Alt+Shift+A"} {
		cfg := Default()
		cfg.Hotkey = old
		data, _ := json.Marshal(cfg)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(data, &fields)
		delete(fields, "ai_hotkey")
		data, _ = json.Marshal(fields)
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		store, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		got := store.Get()
		first, _ := hotkey.Parse(got.Hotkey)
		second, _ := hotkey.Parse(got.AIHotkey)
		if got.Hotkey != old || first == second || got.AIHotkey == "" {
			t.Fatal(got)
		}
		after, _ := os.ReadFile(path)
		if string(data) != string(after) {
			t.Fatal("migration rewrote config")
		}
		if got.HotkeyFor(task.Dictation) != old || got.HotkeyFor(task.Dialogue) != got.AIHotkey {
			t.Fatal("purpose keys swapped")
		}
		if err := store.Save(got); err != nil {
			t.Fatal(err)
		}
		restored, err := Open(path)
		if err != nil || restored.Get() != got {
			t.Fatal("keys not persisted", err)
		}
	}
}
func TestTwoShortcutValidation(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(c *Config) { c.AIHotkey = "" }, func(c *Config) { c.Hotkey = "" },
		func(c *Config) { c.AIHotkey = "alt+control+r" },
		func(c *Config) { c.Hotkey = "Ctrl+Enter"; c.AIHotkey = "CmdOrCtrl+Return" },
		func(c *Config) { c.AIHotkey = "Ctrl+DoesNotExist" },
		func(c *Config) { c.AIHotkey = "CmdOrCtrl+V" },
	} {
		cfg := Default()
		mutate(&cfg)
		if cfg.Validate() == nil {
			t.Fatal("invalid shortcut accepted", cfg.Hotkey, cfg.AIHotkey)
		}
	}
}
