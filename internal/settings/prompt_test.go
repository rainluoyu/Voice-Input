package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptAndBudgetConfigMigration(t *testing.T) {
	for _, tt := range []struct {
		name, data, dictation, dialogue string
		rounds, calls                   int
	}{
		{"missing", `{}`, "", "", 2, 2},
		{"partial", `{"llm":{"model":"legacy"}}`, "", "", 2, 2},
		{"explicit_zero", `{"llm":{"max_tool_rounds":0}}`, "", "", 0, 2},
		{"empty", `{"llm":{"dictation_prompt":"","dialogue_prompt":""}}`, "", "", 2, 2},
		{"whitespace", `{"llm":{"dictation_prompt":" \t\n","dialogue_prompt":"　\r\n"}}`, " \t\n", "　\r\n", 2, 2},
		{"custom", `{"llm":{"dictation_prompt":"润色业务","dialogue_prompt":"对话业务","max_tool_rounds":8},"exa":{"max_calls":8}}`, "润色业务", "对话业务", 8, 8},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.data), 0600); err != nil {
				t.Fatal(err)
			}
			store, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg := store.Get()
			if cfg.LLM.DictationPrompt != tt.dictation || cfg.LLM.DialoguePrompt != tt.dialogue || cfg.LLM.MaxToolRounds != tt.rounds || cfg.Exa.MaxCalls != tt.calls || cfg.LLM.TimeoutSeconds != 45 {
				t.Fatalf("migration lost defaults or explicit values: %+v", cfg.LLM)
			}
			unchanged, err := os.ReadFile(path)
			if err != nil || string(unchanged) != tt.data {
				t.Fatal("opening rewrote config", err)
			}
			if err := store.Save(cfg); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(path)
			if err != nil || reopened.Get() != cfg {
				t.Fatal("new settings did not roundtrip", err)
			}
		})
	}
}

func TestPromptAndBudgetConfigValidation(t *testing.T) {
	for _, field := range []string{"dictation", "dialogue"} {
		for _, tt := range []struct {
			name, text string
			valid      bool
		}{
			{"empty", "", true}, {"whitespace", " \t\n　", true},
			{"unicode_limit", strings.Repeat("📦", 16000), true},
			{"too_long", strings.Repeat("字", 16001), false},
			{"NUL", "a\x00b", false}, {"invalid_UTF8", "a\xffb", false},
		} {
			t.Run(field+"/"+tt.name, func(t *testing.T) {
				cfg := Default()
				if field == "dictation" {
					cfg.LLM.DictationPrompt = tt.text
				} else {
					cfg.LLM.DialoguePrompt = tt.text
				}
				if err := cfg.Validate(); (err == nil) != tt.valid {
					t.Fatalf("valid=%v: %v", tt.valid, err)
				}
			})
		}
	}
	for value := -1; value <= 9; value++ {
		t.Run(fmt.Sprintf("rounds_%d", value), func(t *testing.T) {
			cfg := Default()
			cfg.LLM.MaxToolRounds = value
			if err := cfg.Validate(); (err == nil) != (value >= 0 && value <= 8) {
				t.Fatalf("rounds=%d: %v", value, err)
			}
		})
		t.Run(fmt.Sprintf("calls_%d", value), func(t *testing.T) {
			cfg := Default()
			cfg.Exa.MaxCalls = value
			if err := cfg.Validate(); (err == nil) != (value >= 1 && value <= 8) {
				t.Fatalf("calls=%d: %v", value, err)
			}
		})
	}
}

func TestMaximumPromptsAndBackgroundRoundTrip(t *testing.T) {
	for _, text := range []string{strings.Repeat("📦", 16000), strings.Repeat("<\x01", 8000)} {
		path := filepath.Join(t.TempDir(), "config.json")
		store, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg := store.Get()
		cfg.LLM.DictationPrompt, cfg.LLM.DialoguePrompt, cfg.Profile.CustomBackground = text, text, text
		if err := store.Save(cfg); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) <= 128<<10 || len(data) > maxConfigBytes {
			t.Fatal("fixture did not exercise expanded capacity", len(data), err)
		}
		reopened, err := Open(path)
		if err != nil || reopened.Get() != cfg {
			t.Fatal("maximum text did not roundtrip", err)
		}
	}
}

func TestSerializedConfigCapacityCheckedBeforeSaving(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()
	cfg.LLM.BaseURL = "https://example.com/"
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// A '<' takes six JSON bytes; fill the remainder with plain path bytes.
	remaining := maxConfigBytes - len(data) - 1
	cfg.LLM.BaseURL += strings.Repeat("<", remaining/6) + strings.Repeat("a", remaining%6)
	if err := cfg.Validate(); err != nil {
		t.Fatal("exact serialized limit rejected", err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil || len(before) != maxConfigBytes {
		t.Fatal("serialized fixture not at limit", len(before), err)
	}
	reopened, err := Open(path)
	if err != nil || reopened.Get() != cfg {
		t.Fatal("saved config cannot reopen", err)
	}
	tooLarge := cfg
	tooLarge.LLM.BaseURL += "x"
	if tooLarge.Validate() == nil {
		t.Fatal("oversized actual JSON accepted before shortcut update")
	}
	if store.Save(tooLarge) == nil || store.Get() != cfg {
		t.Fatal("oversized save changed memory")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("oversized save changed file", err)
	}
}

func TestConfigReadCapacity(t *testing.T) {
	for _, size := range []int{maxConfigBytes, maxConfigBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			data := "{}" + strings.Repeat(" ", size-2)
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Open(path)
			if (err == nil) != (size == maxConfigBytes) {
				t.Fatal("read capacity mismatch", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != data {
				t.Fatal("open changed original file", err)
			}
		})
	}
}
