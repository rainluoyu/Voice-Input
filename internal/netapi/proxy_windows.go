//go:build windows

package netapi

import (
	"errors"
	"net/http"
	"net/url"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	winHTTPAccessNoProxy    uint32 = 1
	winHTTPAccessNamedProxy uint32 = 3
	winHTTPAutoDetect       uint32 = 1
	winHTTPAutoConfigURL    uint32 = 2
	winHTTPDetectDHCP       uint32 = 1
	winHTTPDetectDNSA       uint32 = 2
	winHTTPAutoDetectError         = syscall.Errno(12180)
)

// BOOL is a signed 32-bit integer even on Windows/amd64. Pointer fields use
// native pointer alignment; in particular these are not packed Go structs.
type winHTTPCurrentUserConfig struct {
	autoDetect    int32
	autoConfigURL *uint16
	proxy         *uint16
	bypass        *uint16
}

type winHTTPAutoProxyOptions struct {
	flags           uint32
	autoDetectFlags uint32
	autoConfigURL   *uint16
	reserved        unsafe.Pointer
	reservedFlags   uint32
	autoLogon       int32
}

type winHTTPProxyInfo struct {
	accessType uint32
	proxy      *uint16
	bypass     *uint16
}

// A local, typed call table lets resource paths be tested without loading a DLL.
// It is not a mutable package-wide test hook.
type winHTTPNative struct {
	load        func() error
	getConfig   func(*winHTTPCurrentUserConfig) error
	open        func() (uintptr, error)
	setTimeouts func(uintptr, int32) error
	getProxy    func(uintptr, *uint16, *winHTTPAutoProxyOptions, *winHTTPProxyInfo) error
	closeHandle func(uintptr) error
	globalFree  func(*uint16) error
}

var (
	winProxyWinHTTP = windows.NewLazySystemDLL("winhttp.dll")
	winProxyKernel  = windows.NewLazySystemDLL("kernel32.dll")
	winProxyGate    = make(chan struct{}, 1)
)

func systemProxy() func(*http.Request) (*url.URL, error) {
	return (winSystemProxyResolver{native: newWinHTTPNative(), gate: winProxyGate}).proxy
}

func newWinHTTPNative() winHTTPNative {
	getConfig := winProxyWinHTTP.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
	open := winProxyWinHTTP.NewProc("WinHttpOpen")
	setTimeouts := winProxyWinHTTP.NewProc("WinHttpSetTimeouts")
	getProxy := winProxyWinHTTP.NewProc("WinHttpGetProxyForUrl")
	closeHandle := winProxyWinHTTP.NewProc("WinHttpCloseHandle")
	globalFree := winProxyKernel.NewProc("GlobalFree")
	return winHTTPNative{
		load: func() error {
			for _, proc := range []*windows.LazyProc{getConfig, open, setTimeouts, getProxy, closeHandle, globalFree} {
				if err := proc.Find(); err != nil {
					return err
				}
			}
			return nil
		},
		getConfig: func(out *winHTTPCurrentUserConfig) error {
			ok, _, err := getConfig.Call(uintptr(unsafe.Pointer(out)))
			return winHTTPBoolResult(ok, err)
		},
		open: func() (uintptr, error) {
			session, _, err := open.Call(0, uintptr(winHTTPAccessNoProxy), 0, 0, 0)
			if session == 0 {
				return 0, winHTTPCallError(err)
			}
			return session, nil
		},
		setTimeouts: func(session uintptr, phase int32) error {
			ok, _, err := setTimeouts.Call(session, uintptr(phase), uintptr(phase), uintptr(phase), uintptr(phase))
			return winHTTPBoolResult(ok, err)
		},
		getProxy: func(session uintptr, target *uint16, options *winHTTPAutoProxyOptions, out *winHTTPProxyInfo) error {
			ok, _, err := getProxy.Call(session, uintptr(unsafe.Pointer(target)), uintptr(unsafe.Pointer(options)), uintptr(unsafe.Pointer(out)))
			return winHTTPBoolResult(ok, err)
		},
		closeHandle: func(session uintptr) error {
			ok, _, err := closeHandle.Call(session)
			return winHTTPBoolResult(ok, err)
		},
		globalFree: func(ptr *uint16) error {
			remaining, _, err := globalFree.Call(uintptr(unsafe.Pointer(ptr)))
			if remaining != 0 {
				return winHTTPCallError(err)
			}
			return nil
		},
	}
}

func (n winHTTPNative) currentUserConfig() (config winSystemProxyConfig, err error) {
	if err = n.load(); err != nil {
		return config, err
	}
	var native winHTTPCurrentUserConfig
	defer func() {
		if cleanup := errors.Join(n.freeString(native.autoConfigURL), n.freeString(native.proxy), n.freeString(native.bypass)); cleanup != nil {
			err = errors.Join(err, cleanup)
		}
	}()
	err = n.getConfig(&native)
	runtime.KeepAlive(&native)
	if err != nil {
		return config, err
	}
	return winSystemProxyConfig{
		autoDetect: native.autoDetect != 0, autoConfigURL: windows.UTF16PtrToString(native.autoConfigURL),
		proxy: windows.UTF16PtrToString(native.proxy), bypass: windows.UTF16PtrToString(native.bypass),
	}, nil
}

func (n winHTTPNative) autoProxy(target string, auto winSystemAutoConfig, timeout time.Duration) (info winSystemProxyInfo, err error) {
	if err = n.load(); err != nil {
		return info, err
	}
	targetUTF16, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return info, err
	}
	var options winHTTPAutoProxyOptions
	if auto.url != "" {
		options.flags = winHTTPAutoConfigURL
		options.autoConfigURL, err = windows.UTF16PtrFromString(auto.url)
		if err != nil {
			return info, err
		}
	} else if auto.autoDetect {
		options.flags = winHTTPAutoDetect
		options.autoDetectFlags = winHTTPDetectDHCP | winHTTPDetectDNSA
	} else {
		return info, errors.New("missing automatic proxy configuration")
	}
	// autoLogon remains FALSE: never automatically send the user's credentials.
	session, err := n.open()
	if err != nil {
		return info, err
	}
	defer func() {
		if cleanup := n.closeHandle(session); cleanup != nil {
			err = errors.Join(err, cleanup)
		}
	}()
	if err = n.setTimeouts(session, winHTTPPhaseTimeout(timeout)); err != nil {
		return info, err
	}
	var native winHTTPProxyInfo
	defer func() {
		if cleanup := errors.Join(n.freeString(native.proxy), n.freeString(native.bypass)); cleanup != nil {
			err = errors.Join(err, cleanup)
		}
	}()
	err = n.getProxy(session, targetUTF16, &options, &native)
	// The worker owns every input and the session until this synchronous call
	// returns, even if the request's caller has already stopped waiting.
	runtime.KeepAlive(targetUTF16)
	runtime.KeepAlive(&options)
	runtime.KeepAlive(&native)
	if err != nil {
		if errors.Is(err, winHTTPAutoDetectError) {
			return info, errWinProxyAutoNotFound
		}
		return info, err
	}
	switch native.accessType {
	case winHTTPAccessNoProxy:
		return winSystemProxyInfo{direct: true}, nil
	case winHTTPAccessNamedProxy:
		return winSystemProxyInfo{proxy: windows.UTF16PtrToString(native.proxy), bypass: windows.UTF16PtrToString(native.bypass)}, nil
	default:
		return info, errors.New("unsupported automatic proxy access type")
	}
}

func (n winHTTPNative) freeString(ptr *uint16) error {
	if ptr == nil {
		return nil
	}
	err := n.globalFree(ptr)
	runtime.KeepAlive(ptr)
	return err
}

func winHTTPBoolResult(ok uintptr, err error) error {
	if ok == 0 {
		return winHTTPCallError(err)
	}
	return nil
}

func winHTTPCallError(err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return errors.New("Windows proxy API failed")
	}
	return err
}

func winHTTPPhaseTimeout(timeout time.Duration) int32 {
	if timeout > winSystemProxyLimit {
		timeout = winSystemProxyLimit
	}
	if timeout <= 0 {
		return 1
	}
	return int32((timeout + time.Millisecond - 1) / time.Millisecond)
}
