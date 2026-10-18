package asr

import (
	"errors"
	"net/http"
	"testing"

	"voice_input/internal/netapi"
	"voice_input/internal/settings"
)

func TestAdapterFactory(t *testing.T) {
	cfg := settings.ASR{Endpoint: "https://asr.example/v1/chat/completions", Model: "mimo-v2.5-asr", TimeoutSeconds: 45}
	for _, provider := range []string{"", "openai_compatible", "mimo"} {
		t.Run("provider_"+provider, func(t *testing.T) {
			cfg := cfg
			cfg.Provider = provider
			adapter, err := New(&http.Client{}, cfg, "test-key")
			if err != nil {
				t.Fatal(err)
			}
			switch p := adapter.(type) {
			case Compatible:
				if provider == "mimo" || p.Config != cfg || p.Key != "test-key" {
					t.Fatal("incorrect compatible adapter")
				}
			case MiMo:
				if provider != "mimo" || p.Config != cfg || p.Key != "test-key" {
					t.Fatal("incorrect MiMo adapter")
				}
			default:
				t.Fatalf("unexpected adapter %T", adapter)
			}
		})
	}
	for _, tt := range []struct {
		name   string
		mutate func(*settings.ASR)
	}{
		{"unknown_provider", func(c *settings.ASR) { c.Provider = "unknown" }},
		{"unsafe_endpoint", func(c *settings.ASR) { c.Endpoint = "https://secret@asr.example/v1" }},
		{"missing_model", func(c *settings.ASR) { c.Model = " " }},
		{"model_newline", func(c *settings.ASR) { c.Model = "model\nsecret" }},
		{"invalid_language", func(c *settings.ASR) { c.Provider, c.Language = "mimo", "ja" }},
		{"short_timeout", func(c *settings.ASR) { c.TimeoutSeconds = 0 }},
		{"long_timeout", func(c *settings.ASR) { c.TimeoutSeconds = 181 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := cfg
			tt.mutate(&c)
			p, err := New(&http.Client{}, c, "test-key")
			if p != nil || err == nil {
				t.Fatalf("accepted invalid config: adapter=%T, err=%v", p, err)
			}
			assertASRError(t, err, "config")
		})
	}
	for _, client := range []*http.Client{nil, {}} {
		key := ""
		if client == nil {
			key = "test-key"
		}
		if p, err := New(client, cfg, key); p != nil || err == nil {
			t.Fatal("accepted missing client or key")
		}
	}
}

func assertASRError(t *testing.T, err error, code string) {
	t.Helper()
	var apiErr *netapi.Error
	if !errors.As(err, &apiErr) || apiErr.Service != "asr" || apiErr.Code != code {
		t.Fatalf("want ASR %s error, got %v", code, err)
	}
}
