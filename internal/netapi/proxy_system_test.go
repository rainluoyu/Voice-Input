package netapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeWinSystemNative struct {
	config func() (winSystemProxyConfig, error)
	auto   func(string, winSystemAutoConfig, time.Duration) (winSystemProxyInfo, error)
}

func (f fakeWinSystemNative) currentUserConfig() (winSystemProxyConfig, error) {
	return f.config()
}

func (f fakeWinSystemNative) autoProxy(target string, auto winSystemAutoConfig, limit time.Duration) (winSystemProxyInfo, error) {
	return f.auto(target, auto, limit)
}

func winProxyTestRequest(t *testing.T, raw string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestWinSystemProxySelection(t *testing.T) {
	failure := errors.New("native failure")
	tests := []struct {
		name       string
		config     winSystemProxyConfig
		configErr  error
		info       winSystemProxyInfo
		autoErr    error
		want       string
		wantErr    bool
		wantAuto   bool
		wantDetect bool
		wantPAC    string
	}{
		{name: "none"},
		{name: "no proxy ignores inactive bypass", config: winSystemProxyConfig{bypass: "unsupported://draft"}},
		{name: "static", config: winSystemProxyConfig{proxy: "static:8080"}, want: "http://static:8080"},
		{name: "PAC precedes WPAD and static", config: winSystemProxyConfig{autoConfigURL: "https://config.example/pac", autoDetect: true, proxy: "static:80"}, info: winSystemProxyInfo{proxy: "pac:81"}, want: "http://pac:81", wantAuto: true, wantPAC: "https://config.example/pac"},
		{name: "PAC DIRECT overrides static", config: winSystemProxyConfig{autoConfigURL: "https://config.example/pac", proxy: "static:80"}, info: winSystemProxyInfo{direct: true}, wantAuto: true, wantPAC: "https://config.example/pac"},
		{name: "PAC failure is not direct", config: winSystemProxyConfig{autoConfigURL: "https://config.example/pac", proxy: "static:80"}, autoErr: failure, wantErr: true, wantAuto: true, wantPAC: "https://config.example/pac"},
		{name: "PAC no WPAD is still failure", config: winSystemProxyConfig{autoConfigURL: "https://config.example/pac", autoDetect: true, proxy: "static:80"}, autoErr: errWinProxyAutoNotFound, wantErr: true, wantAuto: true, wantPAC: "https://config.example/pac"},
		{name: "WPAD precedes static", config: winSystemProxyConfig{autoDetect: true, proxy: "static:80"}, info: winSystemProxyInfo{proxy: "wpad:82"}, want: "http://wpad:82", wantAuto: true, wantDetect: true},
		{name: "WPAD DIRECT", config: winSystemProxyConfig{autoDetect: true, proxy: "static:80"}, info: winSystemProxyInfo{direct: true}, wantAuto: true, wantDetect: true},
		{name: "no WPAD falls back to static", config: winSystemProxyConfig{autoDetect: true, proxy: "static:80"}, autoErr: errWinProxyAutoNotFound, want: "http://static:80", wantAuto: true, wantDetect: true},
		{name: "no WPAD and no static is direct", config: winSystemProxyConfig{autoDetect: true}, autoErr: errWinProxyAutoNotFound, wantAuto: true, wantDetect: true},
		{name: "other WPAD failure cannot fall back", config: winSystemProxyConfig{autoDetect: true, proxy: "static:80"}, autoErr: failure, wantErr: true, wantAuto: true, wantDetect: true},
		{name: "no WPAD plus cleanup failure cannot fall back", config: winSystemProxyConfig{autoDetect: true, proxy: "static:80"}, autoErr: errors.Join(errWinProxyAutoNotFound, failure), wantErr: true, wantAuto: true, wantDetect: true},
		{name: "empty automatic result fails", config: winSystemProxyConfig{autoDetect: true}, wantErr: true, wantAuto: true, wantDetect: true},
		{name: "configuration failure", configErr: failure, wantErr: true},
		{name: "PAC bypass is used", config: winSystemProxyConfig{autoDetect: true}, info: winSystemProxyInfo{proxy: "pac:80", bypass: "api.example:443"}, wantAuto: true, wantDetect: true},
		{name: "static bypass does not override PAC", config: winSystemProxyConfig{autoDetect: true, proxy: "static:80", bypass: "api.example"}, info: winSystemProxyInfo{proxy: "pac:80"}, want: "http://pac:80", wantAuto: true, wantDetect: true},
		{name: "static parse failure", config: winSystemProxyConfig{proxy: "socks://proxy:1080"}, wantErr: true},
		{name: "bypass parse failure", config: winSystemProxyConfig{proxy: "proxy:80", bypass: "api.example:bad"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			autoCalls := 0
			native := fakeWinSystemNative{
				config: func() (winSystemProxyConfig, error) { return tt.config, tt.configErr },
				auto: func(target string, auto winSystemAutoConfig, limit time.Duration) (winSystemProxyInfo, error) {
					autoCalls++
					if target != "https://api.example/path" || auto.url != tt.wantPAC || auto.autoDetect != tt.wantDetect {
						t.Errorf("automatic inputs = %q, %+v", target, auto)
					}
					if limit <= 0 || limit > winSystemProxyLimit || limit < 25*time.Second {
						t.Errorf("default resolution budget = %s, want near 30s", limit)
					}
					return tt.info, tt.autoErr
				},
			}
			r := winSystemProxyResolver{native: native, gate: make(chan struct{}, 1)}
			got, err := r.proxy(winProxyTestRequest(t, "https://api.example/path"))
			if errors.Is(err, errSystemProxy) != tt.wantErr || err != nil && !tt.wantErr {
				t.Fatalf("proxy error = %v, want system error %v", err, tt.wantErr)
			}
			value := ""
			if got != nil {
				value = got.String()
			}
			if value != tt.want || (autoCalls == 1) != tt.wantAuto {
				t.Fatalf("proxy = %q, automatic calls = %d; want %q, auto %v", value, autoCalls, tt.want, tt.wantAuto)
			}
		})
	}
}

func TestWinSystemProxyEndpoints(t *testing.T) {
	tests := []struct {
		name, target, proxies, want string
		bad                         bool
	}{
		{name: "bare stays HTTP for HTTPS", target: "https://api.example", proxies: "proxy:8080", want: "http://proxy:8080"},
		{name: "HTTPS key selects target not TLS", target: "https://api.example", proxies: "http=plain:80;https=secure:81", want: "http://secure:81"},
		{name: "HTTP mapping", target: "http://api.example", proxies: "https=secure:81;http=plain:80", want: "http://plain:80"},
		{name: "unrelated FTP mapping", target: "https://api.example", proxies: "ftp=files:21;https=secure:81", want: "http://secure:81"},
		{name: "explicit scheme precedes unsupported SOCKS fallback", target: "https://api.example", proxies: "socks=unknown:1080;https=secure:81", want: "http://secure:81"},
		{name: "unrelated mapped candidate list", target: "https://api.example", proxies: "http=plain:80 spare:81;https=secure:82", want: "http://secure:82"},
		{name: "no applicable scheme", target: "https://api.example", proxies: "http=plain:80"},
		{name: "first applicable", target: "https://api.example", proxies: "http=unused:80;https=first:81;https=second:82", want: "http://first:81"},
		{name: "first unified", target: "https://api.example", proxies: "first:80;second:81", want: "http://first:80"},
		{name: "whitespace candidates", target: "https://api.example", proxies: "first:80 second:81", want: "http://first:80"},
		{name: "whitespace mappings", target: "https://api.example", proxies: "http=plain:80 https=secure:81", want: "http://secure:81"},
		{name: "empty list", target: "https://api.example", proxies: "; ;", bad: true},
		{name: "no failure fallback", target: "https://api.example", proxies: "socks://unknown:1080;valid:80", bad: true},
		{name: "explicit TLS proxy", target: "https://api.example", proxies: "https=https://proxy:443", want: "https://proxy:443"},
		{name: "explicit SOCKS5", target: "https://api.example", proxies: "socks5://proxy:1080", want: "socks5://proxy:1080"},
		{name: "explicit SOCKS5H", target: "https://api.example", proxies: "socks5h://proxy:1080", want: "socks5h://proxy:1080"},
		{name: "IPv6", target: "https://api.example", proxies: "[2001:db8::1]:8080", want: "http://[2001:db8::1]:8080"},
		{name: "default port", target: "https://api.example", proxies: "proxy.example", want: "http://proxy.example"},
		{name: "Unicode proxy hostname", target: "https://api.example", proxies: "代理.example:8080", want: "http://%E4%BB%A3%E7%90%86.example:8080"},
		{name: "unknown SOCKS version", target: "https://api.example", proxies: "socks=proxy:1080", bad: true},
		{name: "raw PAC directive rejected", target: "https://api.example", proxies: "SOCKS proxy:1080", bad: true},
		{name: "userinfo rejected", target: "https://api.example", proxies: "http://user:secret@proxy:80", bad: true},
		{name: "path rejected", target: "https://api.example", proxies: "http://proxy:80/path", bad: true},
		{name: "query rejected", target: "https://api.example", proxies: "http://proxy:80?", bad: true},
		{name: "fragment rejected", target: "https://api.example", proxies: "http://proxy:80#secret", bad: true},
		{name: "zero port", target: "https://api.example", proxies: "proxy:0", bad: true},
		{name: "large port", target: "https://api.example", proxies: "proxy:65536", bad: true},
		{name: "empty port", target: "https://api.example", proxies: "proxy:", bad: true},
		{name: "unbracketed IPv6 rejected", target: "https://api.example", proxies: "2001:db8::1", bad: true},
		{name: "empty mapped endpoint", target: "https://api.example", proxies: "https=", bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := winProxyTestRequest(t, tt.target).URL
			got, err := winSelectSystemProxy(target, tt.proxies, "")
			if (err != nil) != tt.bad {
				t.Fatalf("error = %v, want failure %v", err, tt.bad)
			}
			value := ""
			if got != nil {
				value = got.String()
			}
			if value != tt.want {
				t.Fatalf("proxy = %q, want %q", value, tt.want)
			}
		})
	}
}

func TestWinSystemProxyBypass(t *testing.T) {
	tests := []struct {
		name, target, bypass string
		want, bad            bool
	}{
		{name: "exact case insensitive", target: "https://API.Example", bypass: "api.example", want: true},
		{name: "no DNS suffix expansion", target: "https://api.example", bypass: "example"},
		{name: "leading star", target: "https://a.example", bypass: "*.example", want: true},
		{name: "Unicode bypass hostname", target: "https://api.公司", bypass: "*.公司", want: true},
		{name: "star does not add apex", target: "https://example", bypass: "*.example"},
		{name: "interior glob", target: "https://api-east.example", bypass: "api-*.example", want: true},
		{name: "wildcard all", target: "https://api.example", bypass: "*", want: true},
		{name: "default HTTPS port", target: "https://api.example", bypass: "api.example:443", want: true},
		{name: "default HTTP port", target: "http://api.example", bypass: "api.example:80", want: true},
		{name: "wrong port", target: "https://api.example", bypass: "api.example:80"},
		{name: "explicit port", target: "https://api.example:8443", bypass: "*.example:8443", want: true},
		{name: "numeric port", target: "https://api.example", bypass: "api.example:00443", want: true},
		{name: "numeric target port", target: "https://api.example:00443", bypass: "api.example:443", want: true},
		{name: "IPv6 bracket port", target: "https://[2001:db8::1]:8443", bypass: "[2001:db8:0:0::1]:8443", want: true},
		{name: "IPv6 without port", target: "https://[2001:db8::1]", bypass: "2001:db8::1", want: true},
		{name: "bracket IPv6 without port", target: "https://[2001:db8::1]", bypass: "[2001:db8::1]", want: true},
		{name: "local simple", target: "https://printer", bypass: "<local>", want: true},
		{name: "local does not expand FQDN", target: "https://printer.example", bypass: "<local>"},
		{name: "local excludes trailing dot", target: "https://printer.", bypass: "<local>"},
		{name: "local excludes IPv4", target: "https://192.0.2.1", bypass: "<local>"},
		{name: "local excludes IPv6", target: "https://[2001:db8::1]", bypass: "<local>"},
		{name: "local excludes zoned IPv6", target: "https://[fe80::1%25eth0]", bypass: "<local>"},
		{name: "list separators", target: "https://api.example", bypass: "other; third\tapi.example", want: true},
		{name: "not URL syntax", target: "https://api.example", bypass: "https://api.example", bad: true},
		{name: "unsupported glob", target: "https://api.example", bypass: "api?.example", bad: true},
		{name: "bad port", target: "https://api.example", bypass: "api.example:0", bad: true},
		{name: "invalid later rule is not ignored", target: "https://api.example", bypass: "api.example;bad:port", bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := winSystemProxyBypass(winProxyTestRequest(t, tt.target).URL, tt.bypass)
			if (err != nil) != tt.bad || got != tt.want {
				t.Fatalf("bypass = %v, %v; want %v, failure %v", got, err, tt.want, tt.bad)
			}
		})
	}
}

func TestWinSystemProxyDoesNotReadEnvironment(t *testing.T) {
	for _, key := range []string{"PROXY_URL", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		t.Setenv(key, "invalid://environment")
	}
	r := winSystemProxyResolver{native: fakeWinSystemNative{config: func() (winSystemProxyConfig, error) {
		return winSystemProxyConfig{proxy: "system:8080"}, nil
	}}, gate: make(chan struct{}, 1)}
	got, err := r.proxy(winProxyTestRequest(t, "https://api.example"))
	if err != nil || got == nil || got.String() != "http://system:8080" {
		t.Fatalf("system proxy = %v, %v", got, err)
	}
}

func TestWinSystemProxyLoopback(t *testing.T) {
	r := winSystemProxyResolver{gate: make(chan struct{}, 1)} // A native call would panic.
	for _, target := range []string{"http://localhost", "https://LOCALHOST", "http://127.0.0.1", "http://[::1]"} {
		got, err := r.proxy(winProxyTestRequest(t, target))
		if got != nil || err != nil {
			t.Fatalf("loopback %q = %v, %v", target, got, err)
		}
	}
}

func TestWinSystemProxyCancelledQueueDoesNotStartWorker(t *testing.T) {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	r := winSystemProxyResolver{gate: gate} // A native call would panic.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.proxy(winProxyTestRequest(t, "https://api.example").WithContext(ctx))
	if !errors.Is(err, context.Canceled) || errors.Is(err, errSystemProxy) || len(gate) != 1 {
		t.Fatalf("cancelled queue = %v, occupied slots = %d", err, len(gate))
	}
	<-gate
}

func TestWinSystemProxyCancelledWorkerRetainsSlotUntilCleanup(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var calls, active, peak, cleaned atomic.Int32
	native := fakeWinSystemNative{
		config: func() (winSystemProxyConfig, error) { return winSystemProxyConfig{autoDetect: true}, nil },
		auto: func(_ string, _ winSystemAutoConfig, _ time.Duration) (winSystemProxyInfo, error) {
			current := active.Add(1)
			for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
			}
			defer func() { cleaned.Add(1); active.Add(-1) }()
			if calls.Add(1) == 1 {
				close(started)
				<-release
			}
			return winSystemProxyInfo{direct: true}, nil
		},
	}
	gate := make(chan struct{}, 1)
	r := winSystemProxyResolver{native: native, gate: gate}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	req := winProxyTestRequest(t, "https://api.example").WithContext(ctx)
	go func() { _, err := r.proxy(req); result <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("native worker did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || errors.Is(err, errSystemProxy) {
			t.Fatalf("in-flight cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("caller did not return on cancellation")
	}
	if cleaned.Load() != 0 || active.Load() != 1 || len(gate) != 1 {
		t.Fatal("cancelled caller released a native resource or slot early")
	}

	// Many later timeouts must not create native workers behind the blocked one.
	var waiters sync.WaitGroup
	for range 16 {
		waiters.Go(func() {
			waitCtx, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer stop()
			_, err := r.proxy(req.WithContext(waitCtx))
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("queue deadline = %v", err)
			}
		})
	}
	waiters.Wait()
	if calls.Load() != 1 || active.Load() != 1 || peak.Load() != 1 {
		t.Fatal("native concurrency exceeded its single occupied slot")
	}
	close(release)
	resumeCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if _, err := r.proxy(req.WithContext(resumeCtx)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || cleaned.Load() != 2 || peak.Load() != 1 {
		t.Fatalf("late cleanup calls=%d cleaned=%d peak=%d", calls.Load(), cleaned.Load(), peak.Load())
	}
}

func TestWinSystemProxyUsesEarlierDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	native := fakeWinSystemNative{
		config: func() (winSystemProxyConfig, error) { return winSystemProxyConfig{autoDetect: true}, nil },
		auto: func(_ string, _ winSystemAutoConfig, limit time.Duration) (winSystemProxyInfo, error) {
			if limit <= 0 || limit > 2*time.Second {
				t.Errorf("native budget = %v, want earlier request deadline", limit)
			}
			return winSystemProxyInfo{direct: true}, nil
		},
	}
	r := winSystemProxyResolver{native: native, gate: make(chan struct{}, 1)}
	if _, err := r.proxy(winProxyTestRequest(t, "https://api.example").WithContext(ctx)); err != nil {
		t.Fatal(err)
	}
}

func TestWinSystemProxyContextErrorClassification(t *testing.T) {
	background := context.Background()
	cancelled, cancel := context.WithCancel(background)
	cancel()
	expired, stop := context.WithDeadline(background, time.Now().Add(-time.Second))
	defer stop()
	for _, tt := range []struct {
		name                string
		request, resolution context.Context
		want                error
		wantSystem          bool
	}{
		{name: "live", request: background, resolution: background},
		{name: "request cancellation", request: cancelled, resolution: cancelled, want: context.Canceled},
		{name: "request deadline", request: expired, resolution: expired, want: context.DeadlineExceeded},
		{name: "resolver budget", request: background, resolution: expired, want: context.DeadlineExceeded, wantSystem: true},
		{name: "request error takes priority", request: cancelled, resolution: expired, want: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := winSystemProxyContextError(tt.request, tt.resolution)
			if !errors.Is(err, tt.want) || errors.Is(err, errSystemProxy) != tt.wantSystem {
				t.Fatalf("context error = %v, want %v, system %v", err, tt.want, tt.wantSystem)
			}
			if !tt.wantSystem && err != tt.want {
				t.Fatalf("request context error must be returned unchanged: %v", err)
			}
		})
	}
}

func TestWinSystemProxyInvalidRequest(t *testing.T) {
	r := winSystemProxyResolver{}
	for _, req := range []*http.Request{nil, {}} {
		if _, err := r.proxy(req); !errors.Is(err, errSystemProxy) {
			t.Fatalf("invalid request error = %v", err)
		}
	}
}
