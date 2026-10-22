//go:build windows

package startup

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"voice_input/internal/i18n"
)

var shellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// HandleHelper must be called with os.Args[1:] before acquiring the application's
// single-instance lock or starting its UI. If handled, main must exit(exitCode).
// The helper is one-shot: it accepts only a fixed action and the original SID,
// requires elevation as that same user, performs the action, then returns.
func HandleHelper(args []string) (handled bool, exitCode int) {
	handled, action, sid, valid := parseHelper(args)
	if !handled {
		return false, 0
	}
	if !valid {
		return true, exitFailed
	}
	requested, err := windows.StringToSid(sid)
	if err != nil || requested.String() != sid {
		return true, exitIdentity
	}
	current, elevated, err := identity()
	if err != nil || !elevated || current != sid {
		// Credential prompts filled with a different administrator account are
		// deliberately rejected before either user's registration is accessed.
		return true, exitIdentity
	}
	exe, err := executable()
	if err != nil {
		return true, exitFailed
	}
	b := &windowsBackend{exe: exe, sid: sid}
	if err := b.changeTask(action == ensureAdmin); err != nil {
		return true, exitFailed
	}
	return true, 0
}

func identityError(err error) error { return i18n.Wrap(err, "startup.identity_failed") }

// shellExecuteInfo mirrors SHELLEXECUTEINFOW, including the pointer-sized union.
// Natural Go alignment matches both the 32-bit and 64-bit Windows ABI.
type shellExecuteInfo struct {
	size       uint32
	mask       uint32
	window     windows.Handle
	verb       *uint16
	file       *uint16
	parameters *uint16
	directory  *uint16
	show       int32
	instance   windows.Handle
	idList     uintptr
	class      *uint16
	classKey   windows.Handle
	hotKey     uint32
	icon       windows.Handle
	process    windows.Handle
}

func elevate(exe, action, sid string) error {
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	parameters, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{helperFlag, action, sid}))
	if err != nil {
		return err
	}
	info := shellExecuteInfo{
		// SEE_MASK_NOCLOSEPROCESS | SEE_MASK_NOASYNC | SEE_MASK_FLAG_NO_UI
		mask: 0x40 | 0x100 | 0x400,
		verb: verb, file: file, parameters: parameters,
		// HWND=0; the settings Options intentionally carries no native window.
	}
	info.size = uint32(unsafe.Sizeof(info))
	ok, _, callErr := shellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(info)
	if ok == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return i18n.Wrap(callErr, "startup.cancelled")
		}
		if callErr == syscall.Errno(0) {
			callErr = errors.New("ShellExecuteExW failed without a last-error code")
		}
		return callErr
	}
	if info.process == 0 {
		return errors.New("startup helper returned no process handle")
	}
	defer windows.CloseHandle(info.process)
	status, err := windows.WaitForSingleObject(info.process, windows.INFINITE)
	if err != nil {
		return err
	}
	if status != windows.WAIT_OBJECT_0 {
		return errors.New("unexpected startup helper wait status")
	}
	var exit uint32
	if err := windows.GetExitCodeProcess(info.process, &exit); err != nil {
		return err
	}
	switch exit {
	case 0:
		return nil
	case exitIdentity:
		return identityError(nil)
	default:
		return i18n.Error("startup.failed")
	}
}
