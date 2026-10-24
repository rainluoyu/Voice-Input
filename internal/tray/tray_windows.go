//go:build windows && amd64

package tray

import (
	"errors"
	"fmt"
	"unicode/utf16"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

const (
	className       = "voice_input.Tray"
	iconID          = 1
	callbackMessage = w32.WM_APP + 1
	retryTimer      = 1
	retryMillis     = 1000
)

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	registerClass   = user32.NewProc("RegisterClassExW")
	createWindow    = user32.NewProc("CreateWindowExW")
	messageFilter   = user32.NewProc("ChangeWindowMessageFilterEx")
	setTimer        = user32.NewProc("SetTimer")
	notifyIcon      = windows.NewLazySystemDLL("shell32.dll").NewProc("Shell_NotifyIconW")
	windowCallback  = windows.NewCallback(windowProc)
	windowsByHandle = map[uintptr]*Window{}
)

type Options struct {
	Icon    []byte
	Tooltip string
	Menu    *application.Menu
	OnClick func()
	OnError func(error)
}

type popupMenu interface {
	ShowAtCursor()
	ProcessCommand(int) bool
	Destroy()
}

// All methods and callbacks run on the application's native UI thread.
// The invisible top-level window, unlike HWND_MESSAGE, receives shell broadcasts.
type Window struct {
	options        Options
	hwnd, icon     uintptr
	taskbarCreated uint32
	menu           popupMenu
	registration   registration
	timer, warned  bool
	lastError      error
}

func New(options Options) *Window { return &Window{options: options} }

func (w *Window) Start() (err error) {
	if w.registration.closed {
		return errors.New("tray is closed")
	}
	if w.hwnd != 0 {
		return nil
	}
	defer func() {
		if err != nil {
			w.Destroy()
		}
	}()
	name := w32.MustStringToUTF16Ptr(className)
	instance := w32.GetModuleHandle("")
	wc := w32.WNDCLASSEX{Size: uint32(unsafe.Sizeof(w32.WNDCLASSEX{})),
		WndProc: windowCallback, Instance: instance, ClassName: name}
	atom, _, callErr := registerClass.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 && !errors.Is(callErr, windows.ERROR_CLASS_ALREADY_EXISTS) {
		return callError("RegisterClassExW", callErr)
	}
	w.hwnd, _, callErr = createWindow.Call(w32.WS_EX_TOOLWINDOW, uintptr(unsafe.Pointer(name)),
		0, w32.WS_POPUP, 0, 0, 0, 0, 0, 0, instance, 0)
	if w.hwnd == 0 {
		return callError("CreateWindowExW", callErr)
	}
	windowsByHandle[w.hwnd] = w
	w.taskbarCreated = w32.RegisterWindowMessage(w32.MustStringToUTF16Ptr("TaskbarCreated"))
	if w.taskbarCreated == 0 {
		return callError("RegisterWindowMessageW", windows.GetLastError())
	}
	// Explorer normally runs at medium integrity, even when this app is elevated.
	for _, message := range []uint32{w.taskbarCreated, callbackMessage} {
		ok, _, callErr := messageFilter.Call(w.hwnd, uintptr(message), 1, 0) // MSGFLT_ALLOW
		if ok == 0 {
			return callError(fmt.Sprintf("ChangeWindowMessageFilterEx(%#x)", message), callErr)
		}
	}
	w.icon, err = w32.CreateSmallHIconFromImage(w.options.Icon)
	if err != nil || w.icon == 0 {
		return callError("CreateSmallHIconFromImage", err)
	}
	w.menu = application.NewPopupMenu(w.hwnd, w.options.Menu)
	w.registration.notify = w.notify
	return w.restore()
}

func callError(operation string, err error) error {
	if err == nil || errors.Is(err, windows.ERROR_SUCCESS) {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func (w *Window) notifyData() w32.NOTIFYICONDATA {
	data := w32.NOTIFYICONDATA{CbSize: uint32(unsafe.Sizeof(w32.NOTIFYICONDATA{})),
		HWnd: w.hwnd, UID: iconID, UFlags: w32.NIF_MESSAGE | w32.NIF_ICON | w32.NIF_TIP | w32.NIF_SHOWTIP,
		UCallbackMessage: callbackMessage, HIcon: w.icon}
	tip := utf16.Encode([]rune(w.options.Tooltip))
	n := min(len(tip), len(data.SzTip)-1)
	if n > 0 && tip[n-1] >= 0xd800 && tip[n-1] <= 0xdbff {
		n-- // Do not truncate a supplementary character between its surrogates.
	}
	copy(data.SzTip[:], tip[:n])
	return data
}

func (w *Window) notify(operation operation) bool {
	data := w.notifyData()
	if operation == setVersion {
		data.UVersion = w32.NOTIFYICON_VERSION_4
	}
	ok, _, err := notifyIcon.Call(uintptr(operation), uintptr(unsafe.Pointer(&data)))
	if ok == 0 {
		w.lastError = callError(fmt.Sprintf("Shell_NotifyIconW(%d)", operation), err)
	}
	return ok != 0
}

func (w *Window) restore() error {
	switch w.registration.restore() {
	case ready, stopped:
		w.stopTimer()
	case retry:
		if !w.timer {
			id, _, err := setTimer.Call(w.hwnd, retryTimer, retryMillis, 0)
			if id == 0 {
				return callError("SetTimer", err)
			}
			w.timer = true
		}
	case unavailable:
		w.stopTimer()
		return w.lastError
	}
	return nil
}

func (w *Window) report(err error) {
	if err != nil && !w.warned && !w.registration.closed {
		w.warned = true
		if w.options.OnError != nil {
			w.options.OnError(err)
		}
	}
}

func (w *Window) SetTooltip(tooltip string) {
	w.options.Tooltip = tooltip
	if w.registration.added && !w.registration.closed {
		w.report(w.restore())
	}
}

func (w *Window) stopTimer() {
	if w.timer {
		w32.KillTimer(w.hwnd, retryTimer)
		w.timer = false
	}
}

func (w *Window) cleanup() {
	if w.registration.closed {
		return
	}
	w.stopTimer()
	w.registration.close()
	if w.menu != nil {
		w.menu.Destroy()
		w.menu = nil
	}
	if w.icon != 0 {
		w32.DestroyIcon(w.icon)
		w.icon = 0
	}
}

func (w *Window) Destroy() {
	w.cleanup()
	if w.hwnd != 0 {
		w32.DestroyWindow(w.hwnd)
	}
}

func (w *Window) onNotification(wp, lp uintptr) {
	if !w.registration.added || w.registration.closed {
		return
	}
	switch notificationFor(iconID, wp, lp) {
	case activate:
		if w.options.OnClick != nil {
			w.options.OnClick()
		}
	case openMenu:
		if w.menu != nil {
			w.menu.ShowAtCursor()
		}
	}
}

func windowProc(hwnd uintptr, message uint32, wp, lp uintptr) uintptr {
	w := windowsByHandle[hwnd]
	if w != nil {
		switch message {
		case w.taskbarCreated:
			if !w.registration.closed && !w.timer {
				w.registration.failures = 0
				w.report(w.restore())
			}
			return 0
		case w32.WM_TIMER:
			if wp == retryTimer && w.timer && !w.registration.closed {
				w.report(w.restore())
			}
			return 0
		case callbackMessage:
			w.onNotification(wp, lp)
			return 0
		case w32.WM_COMMAND:
			if !w.registration.closed && w.menu != nil && w.menu.ProcessCommand(int(wp&0xffff)) {
				return 0
			}
		case w32.WM_DESTROY:
			w.cleanup()
		case w32.WM_NCDESTROY:
			delete(windowsByHandle, hwnd)
			w.hwnd = 0
		}
	}
	return w32.DefWindowProc(hwnd, message, wp, lp)
}
