//go:build windows

package netapi

import (
	"errors"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWinHTTPStructABI(t *testing.T) {
	pointer := unsafe.Sizeof(uintptr(0))
	var config winHTTPCurrentUserConfig
	if unsafe.Sizeof(config) != 4*pointer || unsafe.Offsetof(config.autoConfigURL) != pointer || unsafe.Offsetof(config.proxy) != 2*pointer || unsafe.Offsetof(config.bypass) != 3*pointer || unsafe.Sizeof(config.autoDetect) != 4 {
		t.Fatal("WINHTTP_CURRENT_USER_IE_PROXY_CONFIG layout mismatch")
	}
	var options winHTTPAutoProxyOptions
	if unsafe.Sizeof(options) != 16+2*pointer || unsafe.Offsetof(options.autoDetectFlags) != 4 || unsafe.Offsetof(options.autoConfigURL) != 8 || unsafe.Offsetof(options.reserved) != 8+pointer || unsafe.Offsetof(options.reservedFlags) != 8+2*pointer || unsafe.Offsetof(options.autoLogon) != 12+2*pointer || unsafe.Sizeof(options.autoLogon) != 4 {
		t.Fatal("WINHTTP_AUTOPROXY_OPTIONS layout mismatch")
	}
	var info winHTTPProxyInfo
	if unsafe.Sizeof(info) != 3*pointer || unsafe.Offsetof(info.proxy) != pointer || unsafe.Offsetof(info.bypass) != 2*pointer || unsafe.Sizeof(info.accessType) != 4 {
		t.Fatal("WINHTTP_PROXY_INFO layout mismatch")
	}
}

type winHTTPTestResources struct {
	allocated map[*uint16]bool
	opened    int
	closed    int
	freed     int
}

func (r *winHTTPTestResources) text(t *testing.T, value string) *uint16 {
	t.Helper()
	ptr, err := windows.UTF16PtrFromString(value)
	if err != nil {
		t.Fatal(err)
	}
	r.allocated[ptr] = true
	return ptr
}

func winHTTPTestNative(t *testing.T) (winHTTPNative, *winHTTPTestResources) {
	t.Helper()
	r := &winHTTPTestResources{allocated: make(map[*uint16]bool)}
	n := winHTTPNative{
		load:      func() error { return nil },
		getConfig: func(*winHTTPCurrentUserConfig) error { return nil },
		open:      func() (uintptr, error) { r.opened++; return 123, nil },
		setTimeouts: func(session uintptr, phase int32) error {
			if session != 123 || phase <= 0 || phase > 30000 {
				t.Errorf("invalid timeout inputs: %d, %d", session, phase)
			}
			return nil
		},
		getProxy: func(uintptr, *uint16, *winHTTPAutoProxyOptions, *winHTTPProxyInfo) error {
			t.Error("unexpected automatic call")
			return errors.New("unexpected automatic call")
		},
		closeHandle: func(session uintptr) error {
			if session != 123 {
				t.Errorf("closed unexpected session %d", session)
			}
			r.closed++
			return nil
		},
		globalFree: func(ptr *uint16) error {
			if !r.allocated[ptr] {
				t.Error("freed unowned pointer or freed a pointer twice")
			}
			delete(r.allocated, ptr)
			r.freed++
			return nil
		},
	}
	return n, r
}

func TestWinHTTPConfigFreesAllStrings(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "partial failure"}[fail], func(t *testing.T) {
			n, resources := winHTTPTestNative(t)
			n.getConfig = func(out *winHTTPCurrentUserConfig) error {
				out.autoDetect = -1 // Any nonzero Win32 BOOL is true.
				out.autoConfigURL = resources.text(t, "https://config.example/pac")
				out.proxy = resources.text(t, "proxy:80")
				out.bypass = resources.text(t, "<local>")
				if fail {
					return syscall.Errno(5)
				}
				return nil
			}
			got, err := n.currentUserConfig()
			if (err != nil) != fail || resources.freed != 3 || len(resources.allocated) != 0 || resources.opened != 0 {
				t.Fatalf("config = %+v, %v; resources = %+v", got, err, resources)
			}
			if !fail && (!got.autoDetect || got.autoConfigURL != "https://config.example/pac" || got.proxy != "proxy:80" || got.bypass != "<local>") {
				t.Fatalf("copied config = %+v", got)
			}
		})
	}
}

func TestWinHTTPAutoResourcesAndOptions(t *testing.T) {
	tests := []struct {
		name       string
		auto       winSystemAutoConfig
		accessType uint32
		callErr    error
		wantErr    bool
		wantDirect bool
	}{
		{name: "PAC", auto: winSystemAutoConfig{url: "https://config.example/pac", autoDetect: true}, accessType: winHTTPAccessNamedProxy},
		{name: "WPAD", auto: winSystemAutoConfig{autoDetect: true}, accessType: winHTTPAccessNamedProxy},
		{name: "DIRECT", auto: winSystemAutoConfig{autoDetect: true}, accessType: winHTTPAccessNoProxy, wantDirect: true},
		{name: "unknown access type", auto: winSystemAutoConfig{autoDetect: true}, wantErr: true},
		{name: "native partial failure", auto: winSystemAutoConfig{autoDetect: true}, callErr: syscall.Errno(12002), wantErr: true},
		{name: "explicit no WPAD", auto: winSystemAutoConfig{autoDetect: true}, callErr: winHTTPAutoDetectError, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, resources := winHTTPTestNative(t)
			n.getProxy = func(session uintptr, target *uint16, options *winHTTPAutoProxyOptions, out *winHTTPProxyInfo) error {
				runtime.GC()
				if session != 123 || windows.UTF16PtrToString(target) != "https://api.example/path" || options.autoLogon != 0 || options.reserved != nil || options.reservedFlags != 0 {
					t.Error("invalid native input or automatic credentials enabled")
				}
				if tt.auto.url != "" {
					if options.flags != winHTTPAutoConfigURL || options.autoDetectFlags != 0 || windows.UTF16PtrToString(options.autoConfigURL) != tt.auto.url {
						t.Errorf("PAC options = %+v", options)
					}
				} else if options.flags != winHTTPAutoDetect || options.autoDetectFlags != winHTTPDetectDHCP|winHTTPDetectDNSA || options.autoConfigURL != nil {
					t.Errorf("WPAD options = %+v", options)
				}
				out.accessType = tt.accessType
				out.proxy = resources.text(t, "proxy:8080")
				out.bypass = resources.text(t, "<local>")
				return tt.callErr
			}
			got, err := n.autoProxy("https://api.example/path", tt.auto, 30*time.Second)
			if (err != nil) != tt.wantErr || got.direct != tt.wantDirect || resources.opened != 1 || resources.closed != 1 || resources.freed != 2 || len(resources.allocated) != 0 {
				t.Fatalf("auto = %+v, %v; resources = %+v", got, err, resources)
			}
			if tt.callErr == winHTTPAutoDetectError && err != errWinProxyAutoNotFound {
				t.Fatalf("no WPAD error = %v", err)
			}
			if !tt.wantErr && !tt.wantDirect && (got.proxy != "proxy:8080" || got.bypass != "<local>") {
				t.Fatalf("copied result = %+v", got)
			}
		})
	}
}

func TestWinHTTPAutoEarlyFailures(t *testing.T) {
	for _, phase := range []string{"load", "target UTF16", "PAC UTF16", "missing configuration", "open", "timeouts"} {
		t.Run(phase, func(t *testing.T) {
			n, resources := winHTTPTestNative(t)
			target := "https://api.example"
			auto := winSystemAutoConfig{url: "https://config.example/pac"}
			failure := errors.New("test failure")
			wantOpen := 0
			wantClose := 0
			switch phase {
			case "load":
				n.load = func() error { return failure }
			case "target UTF16":
				target += "\x00"
			case "PAC UTF16":
				auto.url += "\x00"
			case "missing configuration":
				auto = winSystemAutoConfig{}
			case "open":
				n.open = func() (uintptr, error) { return 0, failure }
			case "timeouts":
				n.setTimeouts = func(uintptr, int32) error { return failure }
				wantOpen, wantClose = 1, 1
			}
			if _, err := n.autoProxy(target, auto, time.Second); err == nil {
				t.Fatal("expected an early error")
			}
			if resources.opened != wantOpen || resources.closed != wantClose || resources.freed != 0 {
				t.Fatalf("early failure resources = %+v", resources)
			}
		})
	}
}

func TestWinHTTPCleanupFailureCannotBecomeNoWPAD(t *testing.T) {
	for _, phase := range []string{"free", "close"} {
		t.Run(phase, func(t *testing.T) {
			n, resources := winHTTPTestNative(t)
			n.getProxy = func(_ uintptr, _ *uint16, _ *winHTTPAutoProxyOptions, out *winHTTPProxyInfo) error {
				out.proxy = resources.text(t, "proxy:80")
				return winHTTPAutoDetectError
			}
			failure := errors.New("cleanup failed")
			if phase == "free" {
				free := n.globalFree
				n.globalFree = func(ptr *uint16) error { _ = free(ptr); return failure }
			} else {
				close := n.closeHandle
				n.closeHandle = func(session uintptr) error { _ = close(session); return failure }
			}
			_, err := n.autoProxy("https://api.example", winSystemAutoConfig{autoDetect: true}, time.Second)
			if err == errWinProxyAutoNotFound || !errors.Is(err, failure) || resources.closed != 1 || resources.freed != 1 {
				t.Fatalf("cleanup failure = %v; resources = %+v", err, resources)
			}
		})
	}
}

func TestWinHTTPTimeoutAndBoolResults(t *testing.T) {
	for _, tt := range []struct {
		input time.Duration
		want  int32
	}{{-1, 1}, {0, 1}, {time.Nanosecond, 1}, {1500 * time.Microsecond, 2}, {2 * time.Second, 2000}, {30 * time.Second, 30000}, {time.Hour, 30000}} {
		if got := winHTTPPhaseTimeout(tt.input); got != tt.want {
			t.Errorf("phase timeout %s = %d, want %d", tt.input, got, tt.want)
		}
	}
	if winHTTPBoolResult(1, syscall.Errno(5)) != nil {
		t.Fatal("successful BOOL call must ignore stale last-error")
	}
	for _, err := range []error{nil, syscall.Errno(0), syscall.Errno(5)} {
		if winHTTPBoolResult(0, err) == nil {
			t.Fatal("failed BOOL call lost its error")
		}
	}
}
