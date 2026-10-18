//go:build windows && amd64

package popup

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	dwmapi          = windows.NewLazySystemDLL("dwmapi.dll")
	dwmSetAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	dwmExtendFrame  = dwmapi.NewProc("DwmExtendFrameIntoClientArea")
	dwmComposition  = dwmapi.NewProc("DwmIsCompositionEnabled")
)

type frameMargins struct{ Left, Right, Top, Bottom int32 }

// DWM returns HRESULT, not a meaningful GetLastError value.
func dwmSucceeded(result uintptr) bool { return int32(uint32(result)) >= 0 }

func backdropAttribute(hwnd uintptr, attribute uint32, value int32) bool {
	if dwmSetAttribute.Find() != nil {
		return false
	}
	result, _, _ := dwmSetAttribute.Call(hwnd, uintptr(attribute), uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
	return dwmSucceeded(result)
}

func extendFrame(hwnd uintptr, margins frameMargins) bool {
	if dwmExtendFrame.Find() != nil {
		return false
	}
	result, _, _ := dwmExtendFrame.Call(hwnd, uintptr(unsafe.Pointer(&margins)))
	return dwmSucceeded(result)
}

// This is the traditional direct-DC custom-frame path, not whole-window alpha.
// Only the empty 6 DIP band is extended. Every inner pixel remains opaque GDI.
// NOACTIVATE may make the system choose a solid inactive material even on success.
func (w *Window) configureBackdrop() {
	w.backdrop = false
	version := windows.RtlGetVersion()
	if version.MajorVersion >= 10 && version.BuildNumber >= 22000 {
		backdropAttribute(w.hwnd, 33, 3) // DWMWA_WINDOW_CORNER_PREFERENCE / DWMWCP_ROUNDSMALL
	}
	rollback := func() {
		extendFrame(w.hwnd, frameMargins{})
		if version.BuildNumber >= 22621 {
			backdropAttribute(w.hwnd, 38, 1) // DWMWA_SYSTEMBACKDROP_TYPE / DWMSBT_NONE
		}
	}
	if version.MajorVersion < 10 || version.BuildNumber < 22621 || dwmComposition.Find() != nil {
		rollback()
		return
	}
	var enabled int32
	result, _, _ := dwmComposition.Call(uintptr(unsafe.Pointer(&enabled)))
	if !dwmSucceeded(result) || enabled == 0 || !backdropAttribute(w.hwnd, 38, 3) { // DWMSBT_TRANSIENTWINDOW
		rollback()
		return
	}
	edge := w.unit(6)
	if !extendFrame(w.hwnd, frameMargins{edge, edge, edge, edge}) {
		rollback()
		return
	}
	w.backdrop = true
}
