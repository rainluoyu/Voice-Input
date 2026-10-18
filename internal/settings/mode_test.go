package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSettingsCanOpenBeforeServicesAreConfigured(t *testing.T) {
	cfg := Default()
	if cfg.Mode != "live" || cfg.Validate() != nil || cfg.ValidateReady() == nil {
		t.Fatal("invalid initial configuration")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal("cannot save unfinished settings", err)
	}
	reopened, err := Open(path)
	if err != nil || reopened.Get() != cfg {
		t.Fatal("unfinished settings cannot be reopened", err)
	}
	cfg.ASR.Endpoint, cfg.ASR.Model = "https://example.com/asr", "asr"
	if cfg.ValidateReady() == nil {
		t.Fatal("missing LLM accepted")
	}
	cfg.LLM.BaseURL, cfg.LLM.Model = "https://example.com/v1", "llm"
	if err := cfg.ValidateReady(); err != nil {
		t.Fatal(err)
	}
	cfg.Mode = "asr_mock_llm"
	cfg.LLM.BaseURL, cfg.LLM.Model = "", ""
	if err := cfg.ValidateReady(); err != nil {
		t.Fatal("ASR diagnostic requires LLM", err)
	}
}
func TestRemovedMockModeMigratesWithoutRewriting(t *testing.T) {
	for _, configured := range []bool{false, true} {
		cfg := Default()
		cfg.Mode = "mock"
		cfg.Profile.CustomBackground = "保留背景"
		cfg.Hotkey = "Ctrl+Shift+R"
		if configured {
			cfg.ASR.Endpoint, cfg.ASR.Model = "https://example.com/asr", "asr"
			cfg.LLM.BaseURL, cfg.LLM.Model = "https://example.com/v1", "llm"
		}
		data, _ := json.Marshal(cfg)
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		store, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		want := cfg
		want.Mode = "live"
		if store.Get() != want {
			t.Fatal("migration changed settings", store.Get())
		}
		after, _ := os.ReadFile(path)
		if string(after) != string(data) {
			t.Fatal("migration rewrote file")
		}
		if err := store.Save(store.Get()); err != nil {
			t.Fatal(err)
		}
		restored, err := Open(path)
		if err != nil || restored.Get() != want {
			t.Fatal("migrated settings not persisted", err)
		}
	}
}
