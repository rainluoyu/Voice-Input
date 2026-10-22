//go:build windows

package startup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	runKey  = `Software\Microsoft\Windows\CurrentVersion\Run`
	runName = "voice_input"
)

var regSetValue = windows.NewLazySystemDLL("advapi32.dll").NewProc("RegSetValueExW")

type windowsBackend struct {
	exe string
	sid string
}

func nativeBackend() (backend, runValue, error) {
	sid, _, err := identity()
	if err != nil {
		return nil, runValue{}, identityError(err)
	}
	exe, err := executable()
	if err != nil {
		return nil, runValue{}, err
	}
	return &windowsBackend{exe: exe, sid: sid}, normalRun(exe), nil
}

func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(exe) || strings.ContainsAny(exe, "\"\x00\r\n") {
		return "", errors.New("invalid startup executable path")
	}
	return filepath.Clean(exe), nil
}

func identity() (sid string, elevated bool, err error) {
	token := windows.GetCurrentProcessToken() // Pseudo-handle, must not be closed.
	user, err := token.GetTokenUser()
	if err != nil {
		return "", false, err
	}
	return user.User.Sid.String(), token.IsElevated(), nil
}

func (*windowsBackend) readRun() (runValue, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return runValue{}, nil
	}
	if err != nil {
		return runValue{}, err
	}
	defer key.Close()
	return readRunValue(key)
}

func readRunValue(key registry.Key) (runValue, error) {
	var data []byte
	for attempts := 0; attempts < 8; attempts++ {
		n, kind, err := key.GetValue(runName, data)
		if errors.Is(err, registry.ErrNotExist) {
			return runValue{}, nil
		}
		if err != nil && !errors.Is(err, registry.ErrShortBuffer) {
			return runValue{}, err
		}
		if err == nil && n <= len(data) {
			return runValue{exists: true, kind: kind, data: data[:n]}, nil
		}
		if n < 0 || n > 1<<20 {
			return runValue{}, errors.New("startup Run value exceeds snapshot limit")
		}
		data = make([]byte, n)
	}
	return runValue{}, errors.New("startup Run value keeps changing")
}

func (*windowsBackend) replaceRun(previous, next runValue) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) && !previous.exists {
		if !next.exists {
			return nil
		}
		key, _, err = registry.CreateKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE|registry.SET_VALUE)
	}
	if err != nil {
		return err
	}
	defer key.Close()
	current, err := readRunValue(key)
	if err != nil {
		return err
	}
	if !current.equal(previous) {
		return errors.New("startup Run value changed since snapshot")
	}
	if current.equal(next) {
		return nil
	}
	if !next.exists {
		err := key.DeleteValue(runName)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	name, err := windows.UTF16PtrFromString(runName)
	if err != nil {
		return err
	}
	var data *byte
	if len(next.data) != 0 {
		data = &next.data[0]
	}
	// registry exposes only typed setters. Use the native API so rollback also
	// preserves non-string and malformed pre-existing values byte for byte.
	code, _, _ := regSetValue.Call(uintptr(key), uintptr(unsafe.Pointer(name)), 0,
		uintptr(next.kind), uintptr(unsafe.Pointer(data)), uintptr(len(next.data)))
	if code != 0 {
		return fmt.Errorf("write startup Run value: %w", syscall.Errno(code))
	}
	return nil
}

func (b *windowsBackend) readTask() (present bool, err error) {
	err = withScheduler(func(service, root *dispatch) error {
		var e error
		present, e = b.inspectTask(service, root)
		return e
	})
	return present, err
}

func (b *windowsBackend) setTask(enabled bool) error {
	present, err := b.readTask()
	if err != nil || present == enabled {
		return err
	}
	sid, elevated, err := identity()
	if err != nil || sid != b.sid {
		return identityError(err)
	}
	if elevated {
		err = b.changeTask(enabled)
	} else {
		action := removeAdmin
		if enabled {
			action = ensureAdmin
		}
		err = elevate(b.exe, action, b.sid)
	}
	if err != nil {
		return err
	}
	present, err = b.readTask()
	if err != nil {
		return err
	}
	if present != enabled {
		return errors.New("startup helper did not apply requested registration")
	}
	return nil
}
