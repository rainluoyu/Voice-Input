//go:build windows && amd64

package popup

import (
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"voice_input/internal/app"
	"voice_input/internal/branding"
	"voice_input/internal/i18n"
)

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	gdi32             = windows.NewLazySystemDLL("gdi32.dll")
	kernel32          = windows.NewLazySystemDLL("kernel32.dll")
	registerClass     = user32.NewProc("RegisterClassExW")
	createWindow      = user32.NewProc("CreateWindowExW")
	defWindowProc     = user32.NewProc("DefWindowProcW")
	destroyWindow     = user32.NewProc("DestroyWindow")
	showWindow        = user32.NewProc("ShowWindow")
	setWindowPos      = user32.NewProc("SetWindowPos")
	isVisible         = user32.NewProc("IsWindowVisible")
	getClientRect     = user32.NewProc("GetClientRect")
	invalidateRect    = user32.NewProc("InvalidateRect")
	beginPaint        = user32.NewProc("BeginPaint")
	endPaint          = user32.NewProc("EndPaint")
	fillRect          = user32.NewProc("FillRect")
	drawText          = user32.NewProc("DrawTextW")
	setTimer          = user32.NewProc("SetTimer")
	killTimer         = user32.NewProc("KillTimer")
	getDPI            = user32.NewProc("GetDpiForWindow")
	monitorFromWindow = user32.NewProc("MonitorFromWindow")
	getMonitorInfo    = user32.NewProc("GetMonitorInfoW")
	getModuleHandle   = kernel32.NewProc("GetModuleHandleW")
	loadCursor        = user32.NewProc("LoadCursorW")
	loadImage         = user32.NewProc("LoadImageW")
	drawIcon          = user32.NewProc("DrawIconEx")
	destroyIcon       = user32.NewProc("DestroyIcon")
	trackMouse        = user32.NewProc("TrackMouseEvent")
	setWindowOrigin   = gdi32.NewProc("SetWindowOrgEx")
	createFont        = gdi32.NewProc("CreateFontW")
	createBrush       = gdi32.NewProc("CreateSolidBrush")
	createPen         = gdi32.NewProc("CreatePen")
	roundRect         = gdi32.NewProc("RoundRect")
	deleteObject      = gdi32.NewProc("DeleteObject")
	selectObject      = gdi32.NewProc("SelectObject")
	setTextColor      = gdi32.NewProc("SetTextColor")
	setBkMode         = gdi32.NewProc("SetBkMode")
	createDC          = gdi32.NewProc("CreateCompatibleDC")
	deleteDC          = gdi32.NewProc("DeleteDC")
	createBitmap      = gdi32.NewProc("CreateCompatibleBitmap")
	bitBlt            = gdi32.NewProc("BitBlt")
	saveDC            = gdi32.NewProc("SaveDC")
	restoreDC         = gdi32.NewProc("RestoreDC")
	clipRect          = gdi32.NewProc("IntersectClipRect")
)

const (
	className     = "voice_input.NonActivatingPopup"
	noActivate    = 0x08000000
	toolWindow    = 0x00000080
	noActivatePos = 0x0010
	background    = 0xf8f6f5 // COLORREF is BGR: #F5F6F8.
	foreground    = 0x2c2420 // #20242C
	muted         = 0x74665d // #5D6674
	blue          = 0xd46324 // #2463D4
	line          = 0xe8e1dc // #DCE1E8
	surface       = 0xffffff
	buttonTint    = surface
	controlBorder = 0xa39387 // #8793A3
	hoverTint     = 0xf4f0ed // #EDF0F4
	pressedTint   = 0xeee7e2 // #E2E7EE
	primaryHover  = 0xb9541d // #1D54B9
	primaryPress  = 0x9d4617 // #17469D
	questionTint  = 0xfef3ed // #EDF3FE
)

type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	SmallIcon                          uintptr
}

type paintStruct struct {
	DC                 uintptr
	Erase              int32
	Paint              rect
	Restore, IncUpdate int32
	Reserved           [32]byte
}

// All methods and callbacks run on the application's native UI thread.
// There are intentionally no child HWNDs: even a native button can call
// SetFocus on mouse-down, despite WS_EX_NOACTIVATE on its parent.
type Window struct {
	hwnd, target                   uintptr
	state                          app.State
	locale                         i18n.Locale
	actions                        Actions
	dpi                            int32
	bodyFont, titleFont, smallFont uintptr
	icon                           uintptr
	backdrop                       bool
	scroll, maxScroll              int32
	pressed, pressedTask           string
	pressedTurn                    uint64
	hover, copyNotice, lastFooter  string
	resultFollow                   resultFollow
}

type Actions struct {
	Close    func()
	Settings func()
	Copy     func(taskID string) error
	KeepOpen func(taskID string)
	Reply    func(taskID string, turn uint64, stop bool)
}

var windowsByHandle = map[uintptr]*Window{}
var windowCallback = windows.NewCallback(windowProc)

func New(actions Actions) *Window { return &Window{actions: actions, dpi: 96, locale: i18n.ZhCN} }

// SetLocale only repaints chrome and application-owned notices. It never shows,
// repositions or recreates a window, or observes the result-follow identity.
func (w *Window) SetLocale(locale i18n.Locale) {
	w.locale = locale
	w.state = app.Present(w.state, locale)
	w.invalidate()
}

func (w *Window) label(code string, args ...string) string {
	if code == "" {
		return ""
	}
	return i18n.M(code, args...).Text(w.locale)
}

func (w *Window) create() error {
	instance, _, _ := getModuleHandle.Call(0)
	name, _ := windows.UTF16PtrFromString(className)
	cursor, _, _ := loadCursor.Call(0, 32512) // IDC_ARROW
	wc := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), Proc: windowCallback,
		Instance: instance, ClassName: name, Cursor: cursor}
	atom, _, err := registerClass.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 && !errors.Is(err, windows.ERROR_CLASS_ALREADY_EXISTS) {
		return fmt.Errorf("注册非激活浮窗: %w", err)
	}
	title, _ := windows.UTF16PtrFromString(branding.Name)
	hwnd, _, err := createWindow.Call(noActivate|toolWindow, uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(title)), 0x80000000, 0, 0,
		uintptr(w.unit(400)), uintptr(w.unit(displayClass(w.state).height())), 0, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("创建非激活浮窗: %w", err)
	}
	w.hwnd = hwnd
	windowsByHandle[hwnd] = w
	w.makeResources()
	w.configureBackdrop()
	setTimer.Call(hwnd, 1, 250, 0)
	return nil
}

func (w *Window) Update(state app.State, target uintptr) error {
	if state.Revision < w.state.Revision {
		return nil
	}
	newTask := state.TaskID != w.state.TaskID
	resized := displayClass(state) != displayClass(w.state)
	copyChanged := (state.Text != "" || state.RawText != "") != (w.state.Text != "" || w.state.RawText != "")
	if resetBodyScroll(w.state, state) {
		w.scroll, w.maxScroll = 0, 0
	}
	if newTask || state.Turn != w.state.Turn || state.Question != w.state.Question || resized || copyChanged {
		w.clearPress()
	}
	if newTask || state.Turn != w.state.Turn || state.Text != w.state.Text || state.RawText != w.state.RawText {
		w.copyNotice = ""
	}
	w.state = state
	w.resultFollow.observe(state)
	if newTask {
		w.hover = ""
		w.target = target
	}
	if !state.Visible {
		if w.hwnd != 0 {
			showWindow.Call(w.hwnd, 0) // SW_HIDE; never activate or restore another window.
		}
		return nil
	}
	if w.hwnd == 0 {
		if dpi, _, _ := getDPI.Call(target); dpi != 0 {
			w.dpi = int32(dpi)
		}
		if err := w.create(); err != nil {
			return err
		}
	}
	visible, _, _ := isVisible.Call(w.hwnd)
	if visible == 0 || newTask {
		w.target = target
	}
	if visible == 0 || newTask || resized {
		w.place()
	}
	ok, _, err := setWindowPos.Call(w.hwnd, ^uintptr(0), 0, 0, 0, 0, // HWND_TOPMOST
		0x0001|0x0002|noActivatePos|0x0040) // NOSIZE | NOMOVE | NOACTIVATE | SHOWWINDOW
	if ok == 0 {
		return fmt.Errorf("显示非激活浮窗: %w", err)
	}
	w.invalidate()
	return nil
}

func (w *Window) Destroy() {
	if w.hwnd != 0 {
		destroyWindow.Call(w.hwnd)
	}
}
func (w *Window) invalidate() {
	if w.hwnd != 0 {
		invalidateRect.Call(w.hwnd, 0, 0)
	}
}
func (w *Window) unit(n int32) int32 { return scale(n, w.dpi) }
func (w *Window) clearPress() {
	w.pressed, w.pressedTask, w.pressedTurn = "", "", 0
}
func (w *Window) layout() layout { return popupLayout(w.state, w.dpi, w.client()) }
func (w *Window) client() rect {
	var r rect
	getClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}
func (w *Window) place() {
	monitor, _, _ := monitorFromWindow.Call(w.target, 2)
	info := struct {
		Size          uint32
		Monitor, Work rect
		Flags         uint32
	}{}
	info.Size = uint32(unsafe.Sizeof(info))
	ok, _, _ := getMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return
	}
	bounds := popupPlacement(w.state, w.dpi, info.Work)
	current := w.client()
	if current.width() != bounds.width() || current.height() != bounds.height() {
		w.clearPress()
	}
	setWindowPos.Call(w.hwnd, 0, uintptr(bounds.Left), uintptr(bounds.Top), uintptr(bounds.width()), uintptr(bounds.height()),
		0x0004|noActivatePos) // NOZORDER | NOACTIVATE
}
func (w *Window) makeResources() {
	w.freeResources()
	name, _ := windows.UTF16PtrFromString("Microsoft YaHei UI")
	makeFont := func(size, weight int32) uintptr {
		height := -w.unit(size)
		font, _, _ := createFont.Call(uintptr(height), 0, 0, 0, uintptr(weight), 0, 0, 0,
			1, 0, 0, 5, 0, uintptr(unsafe.Pointer(name)))
		return font
	}
	w.bodyFont, w.titleFont, w.smallFont = makeFont(14, 400), makeFont(20, 600), makeFont(12, 400)
	instance, _, _ := getModuleHandle.Call(0)
	// Load the existing EXE icon group, without LR_SHARED: we own this handle.
	// Dedicated test binaries may have no resources; the logo is optional.
	w.icon, _, _ = loadImage.Call(instance, 1, 1, uintptr(w.unit(28)), uintptr(w.unit(28)), 0)
}
func (w *Window) freeResources() {
	for _, font := range []uintptr{w.bodyFont, w.titleFont, w.smallFont} {
		if font != 0 {
			deleteObject.Call(font)
		}
	}
	w.bodyFont, w.titleFont, w.smallFont = 0, 0, 0
	if w.icon != 0 {
		destroyIcon.Call(w.icon)
		w.icon = 0
	}
}

func fill(dc uintptr, r rect, color uintptr) {
	brush, _, _ := createBrush.Call(color)
	fillRect.Call(dc, uintptr(unsafe.Pointer(&r)), brush)
	deleteObject.Call(brush)
}
func rounded(dc uintptr, r rect, radius, strokeWidth int32, color, stroke uintptr) {
	if r.width() == 0 || r.height() == 0 {
		return
	}
	brush, _, _ := createBrush.Call(color)
	pen, _, _ := createPen.Call(0, uintptr(max(1, strokeWidth)), stroke)
	if brush == 0 || pen == 0 {
		if brush != 0 {
			deleteObject.Call(brush)
		}
		if pen != 0 {
			deleteObject.Call(pen)
		}
		fill(dc, r, color)
		return
	}
	oldBrush, _, _ := selectObject.Call(dc, brush)
	oldPen, _, _ := selectObject.Call(dc, pen)
	radius = min(radius, min(r.width(), r.height())/2)
	roundRect.Call(dc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom), uintptr(radius*2), uintptr(radius*2))
	selectObject.Call(dc, oldPen)
	selectObject.Call(dc, oldBrush)
	deleteObject.Call(pen)
	deleteObject.Call(brush)
}

func text(dc, font, color uintptr, value string, r rect, flags uintptr) int32 {
	old, _, _ := selectObject.Call(dc, font)
	defer selectObject.Call(dc, old)
	setTextColor.Call(dc, color)
	units := append(utf16.Encode([]rune(value)), 0)
	drawText.Call(dc, uintptr(unsafe.Pointer(&units[0])), uintptr(len(units)-1),
		uintptr(unsafe.Pointer(&r)), flags|0x0800) // DT_NOPREFIX: ampersands are literal.
	return r.Bottom - r.Top
}
func (w *Window) paragraph(dc uintptr, value string, y int32, body rect, font, color uintptr) int32 {
	if body.width() == 0 {
		return y
	}
	r := rect{body.Left, y, body.Right, y}
	height := text(dc, font, color, value, r, 0x0010|0x0400) // WORDBREAK | CALCRECT
	r.Bottom += height
	text(dc, font, color, value, r, 0x0010)
	return y + height
}

func (w *Window) paint() {
	var ps paintStruct
	dc, _, _ := beginPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
	defer endPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
	l := w.layout()
	// Initialize the extended-frame band on the final window DC. Never copy
	// ordinary GDI bitmap pixels over it: compatible bitmaps have no alpha contract.
	frameColor := uintptr(background)
	if w.backdrop {
		frameColor = 0 // Traditional DWM custom-frame black brush, not SetBkMode.
	}
	for _, band := range []rect{
		{l.Outer.Left, l.Outer.Top, l.Outer.Right, l.Inner.Top},
		{l.Outer.Left, l.Inner.Bottom, l.Outer.Right, l.Outer.Bottom},
		{l.Outer.Left, l.Inner.Top, l.Inner.Left, l.Inner.Bottom},
		{l.Inner.Right, l.Inner.Top, l.Outer.Right, l.Inner.Bottom},
	} {
		fill(dc, band, frameColor)
	}
	if l.Inner.width() == 0 || l.Inner.height() == 0 {
		return
	}
	buffer, _, _ := createDC.Call(dc)
	bitmap, _, _ := createBitmap.Call(dc, uintptr(l.Inner.width()), uintptr(l.Inner.height()))
	if buffer == 0 || bitmap == 0 {
		if buffer != 0 {
			deleteDC.Call(buffer)
		}
		if bitmap != 0 {
			deleteObject.Call(bitmap)
		}
		w.draw(dc, l) // Opaque inner fallback, still never touching the frame band.
		return
	}
	old, _, _ := selectObject.Call(buffer, bitmap)
	setWindowOrigin.Call(buffer, uintptr(l.Inner.Left), uintptr(l.Inner.Top), 0)
	w.draw(buffer, l)
	bitBlt.Call(dc, uintptr(l.Inner.Left), uintptr(l.Inner.Top), uintptr(l.Inner.width()), uintptr(l.Inner.height()),
		buffer, uintptr(l.Inner.Left), uintptr(l.Inner.Top), 0x00cc0020)
	selectObject.Call(buffer, old)
	deleteObject.Call(bitmap)
	deleteDC.Call(buffer)
}

func (w *Window) draw(dc uintptr, l layout) {
	// Both the buffer and the direct-DC fallback are clipped to the opaque inner.
	saved, _, _ := saveDC.Call(dc)
	defer restoreDC.Call(dc, saved)
	clipRect.Call(dc, uintptr(l.Inner.Left), uintptr(l.Inner.Top), uintptr(l.Inner.Right), uintptr(l.Inner.Bottom))
	setBkMode.Call(dc, 1) // Text only; this is not how the frame becomes glass.
	for range 2 {
		fill(dc, l.Inner, surface)
		maxScroll, resultBottom := w.drawBody(dc, l)
		w.maxScroll = maxScroll
		next := max(0, min(w.scroll, w.maxScroll))
		if target, ok := w.resultFollow.target(resultBottom, l.Body.height(), w.maxScroll); ok {
			next = target
		}
		if next == w.scroll {
			break
		}
		w.scroll = next
	}
	if w.icon != 0 && l.Icon.width() > 0 && l.Icon.height() > 0 {
		drawIcon.Call(dc, uintptr(l.Icon.Left), uintptr(l.Icon.Top), w.icon,
			uintptr(l.Icon.width()), uintptr(l.Icon.height()), 0, 0, 3) // DI_NORMAL
	}
	text(dc, w.bodyFont, foreground, branding.Name, l.Brand, 0x0020|0x0004)
	text(dc, w.smallFont, muted, purposeLabelForLocale(w.state, w.locale), l.Purpose, 0x0020|0x0004)
	w.button(dc, l.Settings, w.label("popup.button.settings"), "settings", true, false)
	w.button(dc, l.Close, w.label("popup.button.close"), "close", true, false)
	text(dc, w.titleFont, foreground, stageLabelForLocale(w.state, w.locale), l.Stage, 0x0020|0x0004)
	if w.state.Stage == app.Recording {
		bars := meterBars(w.state.Level)
		for i, height := range bars {
			left := l.Meter.Left + int32(i)*l.Meter.width()/9
			right := l.Meter.Left + int32(i+1)*l.Meter.width()/9 - w.unit(4)
			barHeight := min(l.Meter.height(), w.unit(height))
			top := l.Meter.Top + (l.Meter.height()-barHeight)/2
			bar := rect{left, top, max(left, right), top + barHeight}.fit(l.Meter)
			rounded(dc, bar, w.unit(2), w.unit(1), blue, blue)
		}
		level := w.state.Level
		if math.IsNaN(level) || math.IsInf(level, 0) {
			level = 0
		}
		percent := fmt.Sprint(int(math.Round(max(0, min(1, level*8)) * 100)))
		text(dc, w.smallFont, muted, w.label("popup.level", "percent", percent), l.Level, 0x0020)
	}
	fill(dc, rect{l.Body.Left, l.Footer.Top, l.Body.Right, l.Footer.Top + max(1, w.unit(1))}, line)
	w.lastFooter = footerForLocale(w.state, time.Now(), w.locale)
	text(dc, w.smallFont, muted, w.lastFooter, l.FooterStatus, 0x0020|0x0004)
	text(dc, w.smallFont, muted, w.label(w.copyNotice), l.Feedback, 0x0020|0x0002|0x0004)
	if w.maxScroll > 0 {
		text(dc, w.smallFont, muted, w.label("popup.scroll_hint"), l.Hint, 0x0020|0x0004)
	}
	if l.Reply.width() > 0 {
		code, action := "popup.button.processing", replyAction(w.state)
		switch action {
		case "reply-start":
			code = "popup.button.reply_start"
		case "reply-stop":
			code = "popup.button.reply_stop"
		}
		w.button(dc, l.Reply, w.label(code), action, action != "", true)
	}
	if l.Copy.width() > 0 {
		w.button(dc, l.Copy, w.label("popup.button.copy"), "copy", true, true)
	}
}

// One body scrolls Notice, question, raw text, result and sources together.
// resultBottom is recorded before sources, including when sources are much longer.
func (w *Window) drawBody(dc uintptr, l layout) (maxScroll, resultBottom int32) {
	saved, _, _ := saveDC.Call(dc)
	defer restoreDC.Call(dc, saved)
	clipRect.Call(dc, uintptr(l.Body.Left), uintptr(l.Body.Top), uintptr(l.Body.Right), uintptr(l.Body.Bottom))
	y := l.Body.Top - w.scroll
	if w.state.Notice != "" {
		y = w.paragraph(dc, w.state.Notice, y, l.Body, w.bodyFont, muted) + w.unit(16)
	}
	section := func(label, value string, color uintptr) int32 {
		if value == "" {
			return y
		}
		y = w.paragraph(dc, label, y, l.Body, w.smallFont, muted) + w.unit(6)
		y = w.paragraph(dc, value, y, l.Body, w.bodyFont, color)
		bottom := y
		y += w.unit(20)
		return bottom
	}
	if w.state.Question != "" && l.Body.width() > w.unit(24) {
		content := rect{l.Body.Left + w.unit(12), l.Body.Top, l.Body.Right - w.unit(12), l.Body.Bottom}
		measure := rect{content.Left, 0, content.Right, 0}
		height := text(dc, w.smallFont, blue, w.label("popup.section.question"), measure, 0x0010|0x0400) + w.unit(6) +
			text(dc, w.bodyFont, foreground, w.state.Question, measure, 0x0010|0x0400) + w.unit(24)
		rounded(dc, rect{l.Body.Left, y, l.Body.Right, y + height}, w.unit(5), w.unit(1), questionTint, questionTint)
		y += w.unit(12)
		y = w.paragraph(dc, w.label("popup.section.question"), y, content, w.smallFont, blue) + w.unit(6)
		y = w.paragraph(dc, w.state.Question, y, content, w.bodyFont, foreground) + w.unit(32)
	} else {
		section(w.label("popup.section.question"), w.state.Question, foreground)
	}
	rawCode := "popup.section.raw"
	if w.state.Turn > 1 {
		rawCode = "popup.section.raw_with_replies"
	}
	section(w.label(rawCode), w.state.RawText, muted)
	if w.state.Text != "" {
		resultBottom = section(w.label("popup.section.result"), w.state.Text, foreground) + w.scroll - l.Body.Top
	}
	section(w.label("popup.section.sources"), sourceTextForLocale(w.state, w.locale), foreground)
	return max(0, y+w.scroll-l.Body.Bottom), resultBottom
}

func (w *Window) button(dc uintptr, r rect, label, action string, enabled, border bool) {
	if r.width() == 0 || r.height() == 0 {
		return
	}
	primary := action == "reply-start" || action == "reply-stop"
	color, bg := uintptr(foreground), uintptr(buttonTint)
	if primary {
		color, bg = surface, blue
	}
	if !enabled {
		color, bg = muted, background
	} else if action == w.pressed && action == w.hover && w.pressedTask == w.state.TaskID && w.pressedTurn == w.state.Turn {
		bg = pressedTint
		if primary {
			bg = primaryPress
		}
	} else if action == w.hover {
		bg = hoverTint
		if primary {
			bg = primaryHover
		}
	}
	stroke := bg
	if border && !primary {
		stroke = controlBorder
		if !enabled {
			stroke = line
		}
	}
	rounded(dc, r, w.unit(5), w.unit(1), bg, stroke)
	text(dc, w.bodyFont, color, label, r, 0x0020|0x0001|0x0004)
}

func (w *Window) actionAt(x, y int32) string {
	return w.layout().actionAt(w.state, x, y)
}
func (w *Window) click(action string) {
	switch action {
	case "reply-start", "reply-stop":
		if w.actions.Reply != nil {
			w.actions.Reply(w.state.TaskID, w.state.Turn, action == "reply-stop")
		}
	case "close":
		if w.actions.Close != nil {
			w.actions.Close()
		}
	case "settings":
		if w.actions.Settings != nil {
			w.actions.Settings()
		}
	case "copy":
		if w.actions.Copy != nil {
			task, turn := w.state.TaskID, w.state.Turn
			err := w.actions.Copy(task)
			if task != w.state.TaskID || turn != w.state.Turn {
				return
			}
			if err != nil {
				w.copyNotice = "popup.copy.failed"
			} else {
				w.copyNotice = "popup.copy.success"
			}
			w.invalidate()
		}
	}
}
func (w *Window) interact() {
	if w.actions.KeepOpen != nil {
		w.actions.KeepOpen(w.state.TaskID)
	}
}

func windowProc(hwnd uintptr, message uint32, wp, lp uintptr) uintptr {
	w := windowsByHandle[hwnd]
	if w != nil {
		switch message {
		case 0x0021:
			w.interact()
			return 3 // WM_MOUSEACTIVATE -> MA_NOACTIVATE, preserve click.
		case 0x0014:
			return 1 // WM_ERASEBKGND, double-buffered paint.
		case 0x000f:
			w.paint()
			return 0
		case 0x0010:
			w.click("close")
			return 0 // WM_CLOSE cancels instead of destroying.
		case 0x0113: // WM_TIMER: no animation; only a changed countdown repaints.
			if visible, _, _ := isVisible.Call(hwnd); visible != 0 {
				label := footerForLocale(w.state, time.Now(), w.locale)
				if label != w.lastFooter {
					w.lastFooter = label
					r := w.layout().Footer
					invalidateRect.Call(hwnd, uintptr(unsafe.Pointer(&r)), 0)
				}
			}
			return 0
		case 0x0200: // WM_MOUSEMOVE: entering/hovering pins this task until closed.
			w.interact()
			hover := w.actionAt(int32(int16(lp)), int32(int16(lp>>16)))
			if hover != w.hover {
				w.hover = hover
				w.invalidate()
			}
			tracking := struct {
				Size, Flags uint32
				Window      uintptr
				HoverTime   uint32
			}{Flags: 2, Window: hwnd} // TME_LEAVE
			tracking.Size = uint32(unsafe.Sizeof(tracking))
			trackMouse.Call(uintptr(unsafe.Pointer(&tracking)))
			return 0
		case 0x02a3: // WM_MOUSELEAVE
			if w.hover != "" || w.pressed != "" {
				w.hover = ""
				w.clearPress()
				w.invalidate()
			}
			return 0
		case 0x0201: // WM_LBUTTONDOWN
			w.interact()
			w.pressed = w.actionAt(int32(int16(lp)), int32(int16(lp>>16)))
			w.hover = w.pressed
			w.pressedTask, w.pressedTurn = w.state.TaskID, w.state.Turn
			w.invalidate()
			return 0
		case 0x0202: // WM_LBUTTONUP
			action := w.actionAt(int32(int16(lp)), int32(int16(lp>>16)))
			pressed, task, turn := w.pressed, w.pressedTask, w.pressedTurn
			w.clearPress()
			if action != "" && action == pressed && task == w.state.TaskID && turn == w.state.Turn {
				w.click(action)
			}
			w.invalidate()
			return 0
		case 0x020a: // WM_MOUSEWHEEL; no keyboard focus is required.
			w.resultFollow.cancel()
			w.interact()
			w.scroll = max(0, min(w.maxScroll, w.scroll-int32(int16(wp>>16))*w.unit(48)/120))
			w.invalidate()
			return 0
		case 0x0005: // WM_SIZE: invalidate geometry, not the result identity.
			w.clearPress()
			w.invalidate()
			return 0
		case 0x02e0: // WM_DPICHANGED: remain in the target monitor's work area.
			w.dpi = max(1, int32(wp&0xffff))
			w.clearPress()
			w.makeResources()
			w.place()
			w.configureBackdrop()
			w.invalidate()
			return 0
		case 0x031a, 0x031e: // WM_THEMECHANGED / WM_DWMCOMPOSITIONCHANGED
			w.configureBackdrop()
			w.invalidate()
			return 0
		case 0x007e, 0x001a: // WM_DISPLAYCHANGE / WM_SETTINGCHANGE
			w.place()
			w.configureBackdrop()
			w.invalidate()
		case 0x0082: // WM_NCDESTROY
			killTimer.Call(hwnd, 1)
			w.freeResources()
			delete(windowsByHandle, hwnd)
			w.hwnd = 0
		}
	}
	result, _, _ := defWindowProc.Call(hwnd, uintptr(message), wp, lp)
	return result
}
