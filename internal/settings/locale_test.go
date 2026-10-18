package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"voice_input/internal/i18n"
)

func TestUILanguageMigrationDoesNotRewriteStoredBytes(t *testing.T) {
	for _, field := range []string{"", `,"ui_language":""`, `,"ui_language":"auto"`, `,"ui_language":"en"`, `,"ui_language":"zh-CN"`} {
		path := filepath.Join(t.TempDir(), "config.json")
		original := []byte("{\n  \"version\": 1" + field + ", \"asr\":{\"language\":\"zh\"}\n}\n")
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
		store, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		want := "auto"
		if field == `,"ui_language":"en"` {
			want = "en"
		}
		if field == `,"ui_language":"zh-CN"` {
			want = "zh-CN"
		}
		if cfg := store.Get(); cfg.UILanguage != want || cfg.ASR.Language != "zh" {
			t.Fatal("language migration changed ASR", cfg)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(original) {
			t.Fatal("opening configuration rewrote stored bytes", err)
		}
		cfg := store.Get()
		if err := store.Save(cfg); err != nil {
			t.Fatal(err)
		}
		restored, err := Open(path)
		if err != nil || restored.Get() != cfg {
			t.Fatal("language roundtrip", err)
		}
	}
	if Default().UILanguage != "auto" {
		t.Fatal("default must follow system")
	}
}

func TestInvalidUILanguageRejectedWithoutChangingConfiguration(t *testing.T) {
	for _, value := range []string{"EN", "zh", "zh-TW", "fr", " auto", " ", "AUTO"} {
		cfg := Default()
		cfg.UILanguage = value
		if cfg.Normalized().UILanguage != value {
			t.Fatal("invalid nonempty language was silently normalized")
		}
		err := cfg.Validate()
		if err == nil || i18n.FromError(err, "app.init_failed").Code != "settings.ui_language" {
			t.Fatal("invalid language not rejected", value, err)
		}
		path := filepath.Join(t.TempDir(), "config.json")
		bytes, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, bytes, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path); err == nil {
			t.Fatal("invalid stored language accepted")
		}
		after, _ := os.ReadFile(path)
		if string(after) != string(bytes) {
			t.Fatal("invalid file overwritten")
		}
	}
	store, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()
	invalid := cfg
	invalid.UILanguage = "fr"
	if store.Save(invalid) == nil || store.Get() != cfg {
		t.Fatal("invalid save changed memory")
	}
}
