//go:build windows && amd64

package tray

import (
	"errors"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

func TestNotifyDataUsesOwnedIdentityAndFullIcon(t *testing.T) {
	w := New(Options{Tooltip: "voice input · 润色"})
	w.hwnd, w.icon = 123, 456
	data := w.notifyData()
	if unsafe.Sizeof(data) != 976 || data.CbSize != 976 || unsafe.Offsetof(data.UVersion) != 816 {
		t.Fatal("NOTIFYICONDATA x64 layout changed")
	}
	if data.HWnd != w.hwnd || data.UID != iconID || data.HIcon != w.icon || data.UCallbackMessage != callbackMessage {
		t.Fatal("notification does not contain its owned identity and icon", data)
	}
	wantFlags := uint32(w32.NIF_MESSAGE | w32.NIF_ICON | w32.NIF_TIP | w32.NIF_SHOWTIP)
	if data.UFlags != wantFlags || windows.UTF16ToString(data.SzTip[:]) != w.options.Tooltip {
		t.Fatal("notification does not contain its full flags and tooltip")
	}
	w.SetTooltip("最新快捷键")
	data = w.notifyData()
	if windows.UTF16ToString(data.SzTip[:]) != "最新快捷键" {
		t.Fatal("tooltip changed while shell was absent was lost")
	}
}

func TestTooltipIsTerminatedWithoutSplittingSurrogates(t *testing.T) {
	for _, prefix := range []int{125, 126, 127, 128} {
		w := New(Options{Tooltip: strings.Repeat("a", prefix) + "🎤"})
		data := w.notifyData()
		if data.SzTip[len(data.SzTip)-1] != 0 {
			t.Fatal("tooltip is not terminated")
		}
		text := windows.UTF16ToString(data.SzTip[:])
		if strings.ContainsRune(text, '�') || len(utf16.Encode([]rune(text))) > 127 {
			t.Fatal("truncation split a surrogate pair", prefix, text)
		}
	}
}

type fakeMenu struct {
	shown, commands, destroyed int
}

func (m *fakeMenu) ShowAtCursor() { m.shown++ }
func (m *fakeMenu) ProcessCommand(id int) bool {
	if id != 1 {
		return false
	}
	m.commands++
	return true
}
func (m *fakeMenu) Destroy() { m.destroyed++ }

func TestNotificationWndProcDispatch(t *testing.T) {
	const hwnd = 123
	for _, packed := range []bool{false, true} {
		calls := 0
		menu := &fakeMenu{}
		w := New(Options{OnClick: func() { calls++ }})
		w.hwnd, w.menu, w.registration.added = hwnd, menu, true
		windowsByHandle[hwnd] = w
		send := func(event uint32) {
			wp, lp := uintptr(iconID), uintptr(event)
			if packed {
				wp, lp = 123, uintptr(iconID<<16)|uintptr(event)
			}
			windowProc(hwnd, callbackMessage, wp, lp)
		}
		send(w32.WM_LBUTTONUP)
		send(w32.WM_USER)     // NIN_SELECT
		send(w32.WM_USER + 1) // NIN_KEYSELECT
		send(w32.WM_RBUTTONUP)
		send(w32.WM_CONTEXTMENU)
		windowProc(hwnd, w32.WM_COMMAND, 0x120001, 0)
		if calls != 3 || menu.shown != 2 || menu.commands != 1 {
			t.Fatal("WndProc did not dispatch mouse, keyboard or menu notifications", packed, calls, menu)
		}
		windowProc(hwnd, callbackMessage, iconID, 2<<16|w32.WM_LBUTTONUP)
		send(w32.WM_MOUSEMOVE)
		w.registration.added = false
		send(w32.WM_LBUTTONUP)
		send(w32.WM_RBUTTONUP)
		w.registration.added, w.registration.closed = true, true
		send(w32.WM_LBUTTONUP)
		send(w32.WM_CONTEXTMENU)
		windowProc(hwnd, w32.WM_COMMAND, 1, 0)
		delete(windowsByHandle, hwnd)
		if calls != 3 || menu.shown != 2 || menu.commands != 1 {
			t.Fatal("foreign, unknown, unregistered or closed notifications were dispatched", packed, calls, menu)
		}
	}
}

func TestQueuedNotificationsReachWindowProc(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	name := w32.MustStringToUTF16Ptr(className + ".QueueTest")
	instance := w32.GetModuleHandle("")
	wc := w32.WNDCLASSEX{Size: uint32(unsafe.Sizeof(w32.WNDCLASSEX{})),
		WndProc: windowCallback, Instance: instance, ClassName: name}
	if atom, _, err := registerClass.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		t.Fatal(callError("RegisterClassExW", err))
	}
	defer func() {
		user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(name)), instance)
	}()
	calls := 0
	menu := &fakeMenu{}
	w := New(Options{OnClick: func() { calls++ }})
	w.taskbarCreated, w.menu, w.registration.added = 0xc123, menu, true
	var err error
	w.hwnd, _, err = createWindow.Call(w32.WS_EX_TOOLWINDOW, uintptr(unsafe.Pointer(name)),
		0, w32.WS_POPUP, 0, 0, 0, 0, 0, 0, instance, 0)
	if w.hwnd == 0 {
		t.Fatal(callError("CreateWindowExW", err))
	}
	windowsByHandle[w.hwnd] = w
	defer w.Destroy()
	const timeoutTimer, doneMessage = 99, w32.WM_APP + 2
	if w32.SetTimer(w.hwnd, timeoutTimer, 5000, 0) == 0 {
		t.Fatal("could not schedule message pump timeout")
	}
	defer func() { w32.KillTimer(w.hwnd, timeoutTimer) }()
	for _, message := range []struct {
		kind   uint32
		wp, lp uintptr
	}{
		{callbackMessage, iconID, w32.WM_LBUTTONUP},
		{callbackMessage, 123, iconID<<16 | w32.WM_LBUTTONUP},
		{callbackMessage, iconID, w32.WM_RBUTTONUP},
		{callbackMessage, 123, iconID<<16 | w32.WM_CONTEXTMENU},
		{w32.WM_COMMAND, 1, 0},
		{doneMessage, 0, 0},
	} {
		if !w32.PostMessage(w.hwnd, message.kind, message.wp, message.lp) {
			t.Fatal("could not post tray notification")
		}
	}
	for {
		var message w32.MSG
		if result := w32.GetMessage(&message, 0, 0, 0); result <= 0 {
			t.Fatal("message pump exited before tray notification dispatch", result)
		}
		if message.Hwnd == w.hwnd && message.Message == doneMessage {
			break
		}
		if message.Hwnd == w.hwnd && message.Message == w32.WM_TIMER && message.WParam == timeoutTimer {
			t.Fatal("timed out dispatching tray notifications")
		}
		w32.TranslateMessage(&message)
		w32.DispatchMessage(&message)
	}
	if calls != 2 || menu.shown != 2 || menu.commands != 1 {
		t.Fatal("queued notifications did not reach the production WndProc", calls, menu)
	}
}

func TestRecoveryMessagesAreCoalescedAndIgnoredAfterClose(t *testing.T) {
	const hwnd, taskbarCreated = 123, 0xc123
	w := New(Options{})
	w.hwnd, w.taskbarCreated, w.timer = hwnd, taskbarCreated, true
	windowsByHandle[hwnd] = w
	defer delete(windowsByHandle, hwnd)
	calls := 0
	w.registration.notify = func(operation) bool { calls++; return true }
	windowProc(hwnd, taskbarCreated, 0, 0)
	if calls != 0 {
		t.Fatal("broadcast started another recovery while timer was pending")
	}
	w.timer = false
	windowProc(hwnd, taskbarCreated, 0, 0)
	if calls != 1 || !w.registration.added {
		t.Fatal("new shell broadcast did not restore the icon")
	}
	w.registration.closed = true
	windowProc(hwnd, taskbarCreated, 0, 0)
	windowProc(hwnd, w32.WM_TIMER, retryTimer, 0)
	if calls != 1 {
		t.Fatal("queued recovery attempted registration after shutdown")
	}
}

func TestRecoveryFailureIsReportedOnlyOnce(t *testing.T) {
	calls := 0
	w := New(Options{OnError: func(error) { calls++ }})
	w.report(nil)
	w.report(errors.New("shell unavailable"))
	w.report(errors.New("shell still unavailable"))
	if calls != 1 {
		t.Fatal("persistent failure should only open settings once", calls)
	}
	w.registration.closed = true
	w.report(errors.New("closed"))
	if calls != 1 {
		t.Fatal("shutdown should not report queued errors")
	}
	for _, err := range []error{nil, windows.ERROR_SUCCESS} {
		if got := callError("Shell_NotifyIconW", err); got == nil || !strings.Contains(got.Error(), "failed") {
			t.Fatal("Win32 failure without last-error was lost", got)
		}
	}
}
