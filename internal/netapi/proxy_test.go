package netapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"voice_input/internal/i18n"
)

func TestManualProxyValidation(t *testing.T) {
	for _, protocol := range []string{"http", "https", "socks5", "socks5h"} {
		for _, host := range []string{"proxy.example", "proxy.example.", "localhost", "127.0.0.1", "::1", "[2001:db8::1]", "代理.example", "999.1.1.1"} {
			config := ProxyConfig{Enabled: true, Mode: "manual", Protocol: protocol, Host: host, Port: 65535}
			if err := config.Validate(); err != nil {
				t.Fatal("valid manual proxy rejected", config, err)
			}
		}
	}
	for _, tt := range []struct {
		name, code string
		change     func(*ProxyConfig)
	}{
		{"mode", "net.proxy_mode", func(p *ProxyConfig) { p.Mode = "other" }},
		{"protocol", "net.proxy_protocol", func(p *ProxyConfig) { p.Protocol = "ftp" }},
		{"empty_host", "net.proxy_host", func(p *ProxyConfig) { p.Host = "" }},
		{"url", "net.proxy_host", func(p *ProxyConfig) { p.Host = "http://proxy.example" }},
		{"credentials", "net.proxy_host", func(p *ProxyConfig) { p.Host = "user:secret@proxy.example" }},
		{"port_in_host", "net.proxy_host", func(p *ProxyConfig) { p.Host = "proxy.example:8080" }},
		{"path", "net.proxy_host", func(p *ProxyConfig) { p.Host = "proxy.example/path" }},
		{"space", "net.proxy_host", func(p *ProxyConfig) { p.Host = "proxy .example" }},
		{"control", "net.proxy_host", func(p *ProxyConfig) { p.Host = "proxy\x00.example" }},
		{"bracketed_ipv4", "net.proxy_host", func(p *ProxyConfig) { p.Host = "[127.0.0.1]" }},
		{"ipv6_zone", "net.proxy_host", func(p *ProxyConfig) { p.Host = "fe80::1%eth0" }},
		{"empty_label", "net.proxy_host", func(p *ProxyConfig) { p.Host = "proxy..example" }},
		{"bad_label", "net.proxy_host", func(p *ProxyConfig) { p.Host = "-proxy.example" }},
		{"long_label", "net.proxy_host", func(p *ProxyConfig) { p.Host = strings.Repeat("p", 64) + ".example" }},
		{"zero_port", "net.proxy_port", func(p *ProxyConfig) { p.Port = 0 }},
		{"negative_port", "net.proxy_port", func(p *ProxyConfig) { p.Port = -1 }},
		{"large_port", "net.proxy_port", func(p *ProxyConfig) { p.Port = 65536 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := ProxyConfig{Enabled: true, Mode: "manual", Protocol: "http", Host: "proxy.example", Port: 8080}
			tt.change(&config)
			err := config.Validate()
			var descriptor i18n.Descriptor
			if !errors.As(err, &descriptor) || descriptor.I18nMessage().Code != tt.code || strings.Contains(err.Error(), "secret") {
				t.Fatal("missing or unsafe proxy validation", err)
			}
		})
	}
}

func TestInactiveManualProxyDraftDoesNotPreventSaving(t *testing.T) {
	for _, config := range []ProxyConfig{
		{Enabled: false, Mode: "manual", Protocol: "draft", Host: "unfinished://", Port: -1},
		{Enabled: true, Mode: "system", Protocol: "draft", Host: "unfinished://", Port: -1},
	} {
		if err := config.Validate(); err != nil {
			t.Fatal("inactive manual draft should not prevent saving", err)
		}
	}
	config := ProxyConfig{}.Normalized()
	if config.Enabled || config.Mode != "system" || config.Protocol != "http" {
		t.Fatal("normalization enabled a disabled proxy or missed defaults", config)
	}
}

func TestConfiguredProxyPriorityAndLoopback(t *testing.T) {
	for _, mode := range []string{"off", "manual", "system"} {
		t.Run(mode, func(t *testing.T) {
			factoryCalls, resolveCalls := 0, 0
			platform := func() requestProxy {
				factoryCalls++
				return func(*http.Request) (*url.URL, error) {
					resolveCalls++
					return url.Parse("http://system-proxy.example:8888")
				}
			}
			config := DefaultProxyConfig()
			if mode == "off" {
				config.Enabled = false
			} else if mode == "manual" {
				config.Mode, config.Host, config.Port = "manual", "[::1]", 8080
			}
			resolve, err := configuredProxy(config, platform)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "off" {
				if resolve != nil || factoryCalls != 0 {
					t.Fatal("disabled proxy must be nil and must not initialize the platform resolver")
				}
				client, err := Client(config)
				if err != nil || client.Transport.(*http.Transport).Proxy != nil {
					t.Fatal("client inherited a proxy after it was disabled", err)
				}
				return
			}
			for _, target := range []string{"http://localhost:1234/test", "http://LOCALHOST:1234/test", "https://127.0.0.1/test", "http://[::1]:1234/test"} {
				request, _ := http.NewRequest(http.MethodGet, target, nil)
				proxy, err := resolve(request)
				if err != nil || proxy != nil || resolveCalls != 0 {
					t.Fatal("loopback must not reach any proxy", target, proxy, err)
				}
			}
			request, _ := http.NewRequest(http.MethodGet, "https://service.example/resource", nil)
			proxy, err := resolve(request)
			want := "http://[::1]:8080"
			if mode == "system" {
				want = "http://system-proxy.example:8888"
			}
			if err != nil || proxy == nil || proxy.String() != want {
				t.Fatal("wrong selected proxy", proxy, err)
			}
			if mode == "manual" && (factoryCalls != 0 || resolveCalls != 0) || mode == "system" && (factoryCalls != 1 || resolveCalls != 1) {
				t.Fatal("manual and system priority was not enforced", factoryCalls, resolveCalls)
			}
		})
	}
}

type failingSystemProxyTransport struct{}

func (failingSystemProxyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("%w: https://private-pac.example/?key=secret", errSystemProxy)
}

func TestSystemProxyErrorsAreSafeAndRespectRequestContext(t *testing.T) {
	for _, kind := range []string{"system", "cancelled", "timeout"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			var cancel context.CancelFunc = func() {}
			want := "net.proxy_system"
			switch kind {
			case "cancelled":
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = "net.asr.cancelled"
			case "timeout":
				ctx, cancel = context.WithTimeout(ctx, 0)
				want = "net.asr.timeout"
			}
			defer cancel()
			request, _ := http.NewRequest(http.MethodGet, "https://service.example/?key=secret", nil)
			_, err := Do(ctx, &http.Client{Transport: failingSystemProxyTransport{}}, request, "asr", 100)
			var descriptor i18n.Descriptor
			if !errors.As(err, &descriptor) || descriptor.I18nMessage().Code != want || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private-pac") {
				t.Fatal("proxy failure was exposed or misclassified", err)
			}
		})
	}
}

func TestManualProxyIgnoresAmbientProxyVariables(t *testing.T) {
	for _, name := range []string{"PROXY_URL", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		t.Setenv(name, "unsupported://invalid")
	}
	t.Setenv("NO_PROXY", "*")
	for _, protocol := range []string{"http", "https", "socks5", "socks5h"} {
		client, err := Client(ProxyConfig{Enabled: true, Mode: "manual", Protocol: protocol, Host: "manual.example", Port: 1080})
		if err != nil {
			t.Fatal(err)
		}
		request, _ := http.NewRequest(http.MethodGet, "https://service.example", nil)
		proxy, err := client.Transport.(*http.Transport).Proxy(request)
		if err != nil || proxy == nil || proxy.Scheme != protocol || proxy.Host != "manual.example:1080" {
			t.Fatal("ambient variables changed manual configuration", proxy, err)
		}
	}
}
