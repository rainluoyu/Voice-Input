package netapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var errSystemProxy = errors.New("system proxy resolution failed")

// Only this native failure permits WPAD to fall back to the static settings.
var errWinProxyAutoNotFound = errors.New("automatic proxy configuration not found")

const winSystemProxyLimit = 30 * time.Second

type winSystemProxyConfig struct {
	autoDetect    bool
	autoConfigURL string
	proxy         string
	bypass        string
}

type winSystemAutoConfig struct {
	url        string
	autoDetect bool
}

type winSystemProxyInfo struct {
	direct bool
	proxy  string
	bypass string
}

type winSystemProxyNative interface {
	currentUserConfig() (winSystemProxyConfig, error)
	autoProxy(string, winSystemAutoConfig, time.Duration) (winSystemProxyInfo, error)
}

type winSystemProxyResolver struct {
	native winSystemProxyNative
	// The Windows entry point shares one process-wide gate. Tests own their gate.
	gate chan struct{}
}

func (r winSystemProxyResolver) proxy(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, errSystemProxy
	}
	if Loopback(strings.ToLower(req.URL.Hostname())) {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(req.Context(), winSystemProxyLimit)
	defer cancel()
	select {
	case r.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, winSystemProxyContextError(req.Context(), ctx)
	}
	if err := winSystemProxyContextError(req.Context(), ctx); err != nil {
		<-r.gate
		return nil, err
	}

	// Copy the URL before returning on cancellation: the worker owns its input.
	target := *req.URL
	type outcome struct {
		proxy *url.URL
		err   error
	}
	result := make(chan outcome, 1)
	go func() {
		defer func() { <-r.gate }()
		proxy, err := r.resolve(ctx, &target)
		result <- outcome{proxy, err}
	}()
	select {
	case <-ctx.Done():
		return nil, winSystemProxyContextError(req.Context(), ctx)
	case out := <-result:
		if err := winSystemProxyContextError(req.Context(), ctx); err != nil {
			return nil, err
		}
		if out.err != nil {
			return nil, fmt.Errorf("%w: %w", errSystemProxy, out.err)
		}
		return out.proxy, nil
	}
}

func winSystemProxyContextError(request, resolution context.Context) error {
	resolutionErr := resolution.Err()
	if err := request.Err(); err != nil {
		return err
	}
	if resolutionErr != nil {
		// Our own budget is a system resolution failure, not a request timeout.
		return fmt.Errorf("%w: %w", errSystemProxy, resolutionErr)
	}
	return nil
}

func (r winSystemProxyResolver) resolve(ctx context.Context, target *url.URL) (*url.URL, error) {
	cfg, err := r.native.currentUserConfig()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.autoConfigURL != "" || cfg.autoDetect {
		// Explicit PAC never attempts WPAD, and its failures never become DIRECT.
		auto := winSystemAutoConfig{url: cfg.autoConfigURL, autoDetect: cfg.autoConfigURL == ""}
		deadline, _ := ctx.Deadline()
		info, err := r.native.autoProxy(target.String(), auto, time.Until(deadline))
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch {
		case err == nil:
			if info.direct {
				return nil, nil
			}
			if strings.TrimSpace(info.proxy) == "" {
				return nil, errors.New("empty automatic proxy result")
			}
			return winSelectSystemProxy(target, info.proxy, info.bypass)
		case auto.autoDetect && err == errWinProxyAutoNotFound:
			// A positive no-WPAD result is the only automatic fallback.
		default:
			return nil, err
		}
	}
	return winSelectSystemProxy(target, cfg.proxy, cfg.bypass)
}

func winSelectSystemProxy(target *url.URL, proxies, bypass string) (*url.URL, error) {
	if strings.TrimSpace(proxies) == "" {
		return nil, nil
	}
	skip, err := winSystemProxyBypass(target, bypass)
	if err != nil || skip {
		return nil, err
	}
	// WinHTTP uses semicolon/whitespace endpoint lists, not raw PAC directives.
	seen, unsupportedSOCKS := false, false
	for _, group := range strings.Split(proxies, ";") {
		fields := strings.Fields(group)
		if len(fields) > 1 {
			switch strings.ToUpper(fields[0]) {
			case "PROXY", "HTTP", "HTTPS", "SOCKS", "SOCKS4", "SOCKS5", "SOCKS5H", "DIRECT":
				return nil, errors.New("unsupported raw proxy directive")
			}
		}
		mapping := ""
		for _, entry := range fields {
			seen = true
			// A key selects the target protocol, not the proxy's transport.
			if key, value, mapped := strings.Cut(entry, "="); mapped {
				mapping = strings.ToLower(key)
				switch mapping {
				case "http", "https", "ftp", "socks":
				default:
					return nil, errors.New("unsupported system proxy mapping")
				}
				entry = value
			}
			if mapping == "socks" {
				unsupportedSOCKS = true
				continue // Prefer an explicit HTTP(S) mapping over ambiguous SOCKS.
			}
			if mapping != "" && mapping != strings.ToLower(target.Scheme) {
				continue // Bare fallback candidates remain scoped to their mapping.
			}
			return winSystemProxyEndpoint(entry)
		}
	}
	if !seen {
		return nil, errors.New("empty system proxy list")
	}
	if unsupportedSOCKS {
		return nil, errors.New("unsupported system SOCKS version")
	}
	// A valid per-protocol map may have no entry for this target scheme.
	return nil, nil
}

func winSystemProxyEndpoint(raw string) (*url.URL, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid system proxy endpoint")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("unsupported system proxy protocol")
	}
	if u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("invalid system proxy endpoint")
	}
	if _, _, err := winSystemHostPort(u.Host, false); err != nil {
		return nil, err
	}
	return u, nil
}

func winSystemProxyBypass(target *url.URL, bypass string) (bool, error) {
	host := strings.ToLower(target.Hostname())
	ip := net.ParseIP(host)
	if ip != nil {
		host = ip.String()
	}
	port := target.Port()
	if number, err := strconv.Atoi(port); err == nil {
		port = strconv.Itoa(number)
	}
	if port == "" {
		switch strings.ToLower(target.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	matched := false
	for _, rule := range strings.FieldsFunc(bypass, func(r rune) bool { return r == ';' || unicode.IsSpace(r) }) {
		if strings.EqualFold(rule, "<local>") {
			matched = matched || ip == nil && host != "" && !strings.ContainsAny(host, ".:")
			continue
		}
		pattern, rulePort, err := winSystemHostPort(rule, true)
		if err != nil {
			return false, err
		}
		match, _ := path.Match(pattern, host) // The parser permits '*' as the only glob metacharacter.
		matched = matched || match && (rulePort == "" || rulePort == port)
	}
	return matched, nil
}

// Split a Windows endpoint/bypass host without DNS resolution or suffix expansion.
// Brackets are mandatory for an IPv6 proxy endpoint, optional for a bypass IP.
func winSystemHostPort(raw string, glob bool) (host, port string, err error) {
	invalid := func() (string, string, error) { return "", "", errors.New("invalid system proxy host or port") }
	host = raw
	if strings.HasPrefix(raw, "[") {
		end := strings.IndexByte(raw, ']')
		if end < 0 {
			return invalid()
		}
		host = raw[1:end]
		tail := raw[end+1:]
		if tail != "" {
			if !strings.HasPrefix(tail, ":") {
				return invalid()
			}
			port = tail[1:]
			if port == "" {
				return invalid()
			}
		}
		if ip := net.ParseIP(host); ip == nil || !strings.Contains(host, ":") {
			return invalid()
		}
	} else {
		switch strings.Count(raw, ":") {
		case 0:
		case 1:
			host, port, _ = strings.Cut(raw, ":")
			if port == "" {
				return invalid()
			}
		default:
			if !glob || net.ParseIP(raw) == nil {
				return invalid()
			}
		}
	}
	if host == "" {
		return invalid()
	}
	if port != "" {
		for _, c := range port {
			if c < '0' || c > '9' {
				return invalid()
			}
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return invalid()
		}
		port = strconv.Itoa(n)
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), port, nil
	}
	for _, c := range host {
		if !(unicode.IsLetter(c) || unicode.IsDigit(c) || c == '-' || c == '_' || c == '.' || glob && c == '*') {
			return invalid()
		}
	}
	return strings.ToLower(host), port, nil
}
