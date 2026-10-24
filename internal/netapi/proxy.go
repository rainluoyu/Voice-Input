package netapi

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"voice_input/internal/i18n"
)

type ProxyConfig struct {
	Enabled  bool   `json:"enabled"`
	Mode     string `json:"mode"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
}

func DefaultProxyConfig() ProxyConfig {
	return ProxyConfig{Enabled: true, Mode: "system", Protocol: "http"}
}

func (p ProxyConfig) Normalized() ProxyConfig {
	if p.Mode == "" {
		p.Mode = "system"
	}
	if p.Protocol == "" {
		p.Protocol = "http"
	}
	p.Host = strings.TrimSpace(p.Host)
	return p
}

func (p ProxyConfig) Validate() error {
	p = p.Normalized()
	if p.Mode != "system" && p.Mode != "manual" {
		return i18n.Error("net.proxy_mode")
	}
	if !p.Enabled || p.Mode != "manual" {
		return nil
	}
	switch p.Protocol {
	case "http", "https", "socks5", "socks5h":
	default:
		return i18n.Error("net.proxy_protocol")
	}
	if _, err := proxyHost(p.Host); err != nil {
		return err
	}
	if p.Port < 1 || p.Port > 65535 {
		return i18n.Error("net.proxy_port")
	}
	return nil
}

func proxyHost(host string) (string, error) {
	invalid := i18n.Error("net.proxy_host")
	if host == "" || len(host) > 255 || !utf8.ValidString(host) {
		return "", invalid
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		inner := host[1 : len(host)-1]
		if net.ParseIP(inner) == nil || !strings.Contains(inner, ":") {
			return "", invalid
		}
		return inner, nil
	}
	if net.ParseIP(host) != nil {
		return host, nil
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", invalid
		}
		for _, r := range label {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
				return "", invalid
			}
		}
	}
	return host, nil
}

type requestProxy = func(*http.Request) (*url.URL, error)

func configuredProxy(config ProxyConfig, platform func() requestProxy) (requestProxy, error) {
	config = config.Normalized()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if !config.Enabled {
		return nil, nil
	}
	var resolve requestProxy
	if config.Mode == "manual" {
		host, _ := proxyHost(config.Host) // Validate checked the active manual fields.
		proxy := &url.URL{Scheme: config.Protocol, Host: net.JoinHostPort(host, strconv.Itoa(config.Port))}
		resolve = http.ProxyURL(proxy)
	} else {
		resolve = platform()
	}
	return func(request *http.Request) (*url.URL, error) {
		if Loopback(request.URL.Hostname()) {
			return nil, nil
		}
		return resolve(request)
	}, nil
}
