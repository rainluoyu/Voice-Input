package settings

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"voice_input/internal/netapi"
)

func TestProxyConfigurationDefaultsAndExplicitDisable(t *testing.T) {
	defaults := netapi.DefaultProxyConfig()
	disabled := defaults
	disabled.Enabled = false
	manual := netapi.ProxyConfig{Enabled: true, Mode: "manual", Protocol: "http", Host: "proxy.example", Port: 8080}
	for _, tt := range []struct {
		name, json string
		want       netapi.ProxyConfig
	}{
		{"legacy", `{"version":1}`, defaults},
		{"missing_proxy", `{"version":1,"general":{}}`, defaults},
		{"empty_proxy", `{"version":1,"general":{"proxy":{}}}`, defaults},
		{"disabled", `{"version":1,"general":{"proxy":{"enabled":false}}}`, disabled},
		{"disabled_empty_defaults", `{"version":1,"general":{"proxy":{"enabled":false,"mode":"","protocol":""}}}`, disabled},
		{"partial_manual", `{"version":1,"general":{"proxy":{"mode":"manual","host":"proxy.example","port":8080}}}`, manual},
		{"disabled_draft", `{"version":1,"general":{"proxy":{"enabled":false,"mode":"manual","protocol":"unfinished","host":"unfinished://","port":-1}}}`, netapi.ProxyConfig{Enabled: false, Mode: "manual", Protocol: "unfinished", Host: "unfinished://", Port: -1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.json), 0600); err != nil {
				t.Fatal(err)
			}
			store, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := store.Get().General.Proxy; got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != tt.json {
				t.Fatal("opening the configuration rewrote the original file", err)
			}
		})
	}
}

func TestProxyConfigurationRoundTripAndFailedSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	config := store.Get()
	for _, proxy := range []netapi.ProxyConfig{
		{Enabled: true, Mode: "manual", Protocol: "https", Host: "proxy.example", Port: 443},
		{Enabled: true, Mode: "manual", Protocol: "socks5", Host: "::1", Port: 1080},
		{Enabled: false, Mode: "manual", Protocol: "socks5", Host: "::1", Port: 1080},
		{Enabled: true, Mode: "system", Protocol: "http", Host: "incomplete://", Port: 0},
	} {
		config.General.Proxy = proxy
		if err := store.Save(config); err != nil {
			t.Fatal(err)
		}
		reloaded, err := Open(path)
		if err != nil || reloaded.Get() != config {
			t.Fatal("proxy settings or inactive draft did not round trip", err)
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	invalid := config
	invalid.General.Proxy = netapi.ProxyConfig{Enabled: true, Mode: "manual", Protocol: "http", Host: "proxy.example", Port: 0}
	if store.Save(invalid) == nil || store.Get() != config {
		t.Fatal("invalid active proxy changed the saved configuration")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed proxy validation changed the configuration file", err)
	}
}
