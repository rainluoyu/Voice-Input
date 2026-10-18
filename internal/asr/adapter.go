package asr

import (
	"context"
	"net/http"
	"strings"

	"voice_input/internal/netapi"
	"voice_input/internal/settings"
)

// Adapter supports both recognition and a connection-only probe.
type Adapter interface {
	Provider
	Probe(ctx context.Context) error
}

// New selects a protocol without changing the configured complete endpoint.
func New(client *http.Client, cfg settings.ASR, key string) (Adapter, error) {
	if client == nil || netapi.Endpoint(cfg.Endpoint) != nil || strings.TrimSpace(cfg.Model) == "" || len(cfg.Model) > 200 || strings.ContainsAny(cfg.Model, "\r\n") || settings.ValidKey(key) != nil || cfg.TimeoutSeconds < 5 || cfg.TimeoutSeconds > 180 {
		return nil, netapi.Fail("asr", "config")
	}
	switch cfg.Provider {
	case "", "openai_compatible":
		return Compatible{Client: client, Config: cfg, Key: key}, nil
	case "mimo":
		if !mimoLanguage(cfg.Language) {
			return nil, netapi.Fail("asr", "config")
		}
		return MiMo{Client: client, Config: cfg, Key: key}, nil
	default:
		return nil, netapi.Fail("asr", "config")
	}
}
