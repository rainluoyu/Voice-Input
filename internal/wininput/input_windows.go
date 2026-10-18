//go:build windows && amd64

package wininput

import (
	"context"
	"errors"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var getForegroundWindow = user32.NewProc("GetForegroundWindow")
var sendInput = user32.NewProc("SendInput")
var getAsyncKeyState = user32.NewProc("GetAsyncKeyState")

type Native struct{}

// ForegroundWindow is used only to choose a monitor for popup placement.
// Its value never gates processing or determines where text is submitted.
func ForegroundWindow() uintptr {
	hwnd, _, _ := getForegroundWindow.Call()
	return hwnd
}

func modifiersHeld() bool {
	for _, vk := range []uintptr{0x10, 0x11, 0x12, 0x5B, 0x5C} {
		state, _, _ := getAsyncKeyState.Call(vk)
		if state&0x8000 != 0 {
			return true
		}
	}
	return false
}

func (Native) Prepare(ctx context.Context, text string) (func() error, error) {
	plan, err := planInput(text)
	if err != nil {
		return nil, err
	}
	// Do not release the user's held hotkey or turn generated text into shortcuts.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !modifiersHeld() {
			break
		}
		if time.Now().After(deadline) {
			return nil, errors.New("修饰键仍按下；为避免误操作，请复制结果")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return func() error {
		// The low-level helper locks its OS thread and bounds clipboard waiting.
		// Do not use application.Clipboard here: InvokeSync under the controller's
		// commit lock could deadlock with a UI state update.
		return plan.commit(ctx, w32.SetClipboardText, func(inputs []keyboardInput) uint32 {
			sent, _, _ := sendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(keyboardInput{}))
			return uint32(sent)
		}, modifiersHeld)
	}, nil
}
