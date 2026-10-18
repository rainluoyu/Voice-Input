package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRoundTripAndAtomicFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()
	cfg.Profile.CustomBackground = "测试用户，偏好简洁表达"
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(path)
	if err != nil || restored.Get() != cfg {
		t.Fatalf("roundtrip: %v", err)
	}
	invalid := cfg
	invalid.Privacy.LogText = true
	if store.Save(invalid) == nil {
		t.Fatal("accepted unsafe privacy")
	}
	if store.Get() != cfg {
		t.Fatal("failed save changed state")
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "api_key") {
		t.Fatal("key in config")
	}
}
func TestInvalidConfigsNotOverwritten(t *testing.T) {
	for _, data := range []string{`{"version":999}`, `{"key":"secret"}`, `{"mode":"mock","mode":"live"}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		_ = os.WriteFile(path, []byte(data), 0600)
		if _, err := Open(path); err == nil {
			t.Fatal("accepted invalid config")
		}
		after, _ := os.ReadFile(path)
		if string(after) != data {
			t.Fatal("overwrote bad config")
		}
	}
}
func TestConfigValidation(t *testing.T) {
	for _, mutate := range []func(*Config){func(c *Config) { c.Mode = "unknown" }, func(c *Config) { c.Exa.MaxCalls = 9 }, func(c *Config) { c.ASR.Endpoint = "http://outside.example" }, func(c *Config) { c.Profile.CustomBackground = strings.Repeat("字", 16001) }, func(c *Config) { c.Recording.MaxSeconds = 301 }, func(c *Config) { c.Mode = "mock" }, func(c *Config) { c.Privacy.SaveHistory = true }, func(c *Config) { c.LLM.OutputFormat = "text" }} {
		c := Default()
		mutate(&c)
		if c.Validate() == nil {
			t.Fatal("invalid config accepted")
		}
	}
}
func TestKeyBoundaries(t *testing.T) {
	if _, err := KeyName("shell"); err == nil {
		t.Fatal("arbitrary credential target")
	}
	for _, key := range []string{"", "secret\r\nHeader:x", strings.Repeat("x", 4097)} {
		if ValidKey(key) == nil {
			t.Fatal("unsafe key accepted")
		}
	}
}

func TestStorageFailurePreservesConfig(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	original := store.Get()
	if err = store.Save(original); err != nil {
		t.Fatal(err)
	}
	oldFile, _ := os.ReadFile(path)
	blocker := filepath.Join(root, "not-directory")
	_ = os.WriteFile(blocker, []byte("block"), 0600)
	store.path = filepath.Join(blocker, "config.json")
	updated := original
	updated.Profile.CustomBackground = "new"
	if store.Save(updated) == nil {
		t.Fatal("storage error ignored")
	}
	if store.Get() != original {
		t.Fatal("failed storage mutated memory")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(oldFile) {
		t.Fatal("old config changed")
	}
}

func TestASRProviderConfigCompatibility(t *testing.T) {
	for _, tt := range []struct {
		name, asrJSON, provider string
		valid                   bool
	}{
		{"legacy_missing_provider", `{"timeout_seconds":45}`, "openai_compatible", true},
		{"legacy_empty_provider", `{"provider":"","timeout_seconds":45}`, "", true},
		{"compatible", `{"provider":"openai_compatible","timeout_seconds":45}`, "openai_compatible", true},
		{"mimo", `{"provider":"mimo","language":"zh","timeout_seconds":45}`, "mimo", true},
		{"unknown_provider", `{"provider":"unknown","timeout_seconds":45}`, "unknown", false},
		{"unknown_mimo_language", `{"provider":"mimo","language":"ja","timeout_seconds":45}`, "mimo", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			original := `{"version":1,"asr":` + tt.asrJSON + `}`
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			store, err := Open(path)
			if !tt.valid {
				if err == nil {
					t.Fatal("invalid ASR config accepted")
				}
				data, readErr := os.ReadFile(path)
				if readErr != nil || string(data) != original {
					t.Fatal("invalid config overwritten")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cfg := store.Get()
			if cfg.ASR.Provider != tt.provider {
				t.Fatalf("provider = %q, want %q", cfg.ASR.Provider, tt.provider)
			}
			if err := store.Save(cfg); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(path)
			if err != nil || reopened.Get() != cfg {
				t.Fatalf("ASR provider roundtrip: %v", err)
			}
		})
	}
}

func TestASRProviderAndLanguageValidation(t *testing.T) {
	for _, provider := range []string{"", "openai_compatible", "mimo"} {
		for _, language := range []string{"", "auto", "zh", "en", "ja", "zh-CN", "ZH"} {
			t.Run(provider+"/"+language, func(t *testing.T) {
				cfg := Default()
				cfg.ASR.Provider, cfg.ASR.Language = provider, language
				wantError := provider == "mimo" && language != "" && language != "auto" && language != "zh" && language != "en"
				if err := cfg.Validate(); (err != nil) != wantError {
					t.Fatalf("unexpected language validation: %v", err)
				}
			})
		}
	}
	for _, provider := range []string{"unknown", "MiMo", "mimo ", "openai"} {
		cfg := Default()
		cfg.ASR.Provider = provider
		if cfg.Validate() == nil {
			t.Fatal("unknown provider accepted")
		}
	}
	if Default().ASR.Provider != "openai_compatible" {
		t.Fatal("default provider changed")
	}
}
