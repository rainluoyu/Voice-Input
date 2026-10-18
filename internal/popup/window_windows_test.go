//go:build windows && amd64

package popup

import (
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"voice_input/internal/app"
	"voice_input/internal/i18n"
	"voice_input/internal/search"
)

type message struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}

func pump() {
	peek := user32.NewProc("PeekMessageW")
	translate := user32.NewProc("TranslateMessage")
	dispatch := user32.NewProc("DispatchMessageW")
	until := time.Now().Add(80 * time.Millisecond)
	for time.Now().Before(until) {
		var m message
		for {
			ok, _, _ := peek.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1)
			if ok == 0 {
				break
			}
			translate.Call(uintptr(unsafe.Pointer(&m)))
			dispatch.Call(uintptr(unsafe.Pointer(&m)))
		}
		time.Sleep(time.Millisecond)
	}
}

func centerLParam(r rect) uintptr {
	return uintptr(uint16((r.Left+r.Right)/2)) | uintptr(uint16((r.Top+r.Bottom)/2))<<16
}

func TestNativeWindowABISizes(t *testing.T) {
	if unsafe.Sizeof(windowClass{}) != 80 || unsafe.Sizeof(paintStruct{}) != 72 || unsafe.Sizeof(message{}) != 48 {
		t.Fatalf("unexpected Win32 layouts: WNDCLASSEX=%d PAINTSTRUCT=%d MSG=%d",
			unsafe.Sizeof(windowClass{}), unsafe.Sizeof(paintStruct{}), unsafe.Sizeof(message{}))
	}
}

func TestDWMHRESULTAndMarginABI(t *testing.T) {
	for _, result := range []uintptr{0, 1, 0x7fffffff} {
		if !dwmSucceeded(result) {
			t.Fatalf("successful HRESULT rejected: %#x", result)
		}
	}
	for _, result := range []uintptr{0x80004005, 0x80070057, 0xffffffff, 0xffffffff80004005} {
		if dwmSucceeded(result) {
			t.Fatalf("failed HRESULT accepted: %#x", result)
		}
	}
	if unsafe.Sizeof(frameMargins{}) != 16 {
		t.Fatal("wrong DWM MARGINS ABI")
	}
}

func TestCopyFeedbackCannotOverwriteNoticeOrNewTask(t *testing.T) {
	w := New(Actions{})
	w.state = app.State{TaskID: "task", Turn: 1, Notice: "原始任务错误"}
	w.actions.Copy = func(id string) error {
		if id != "task" {
			t.Fatal("copy callback contract changed", id)
		}
		return errors.New("clipboard busy")
	}
	w.click("copy")
	if w.label(w.copyNotice) != "复制失败，请重试" || w.state.Notice != "原始任务错误" {
		t.Fatal("failed copy replaced task Notice")
	}
	w.actions.Copy = func(string) error { return nil }
	w.click("copy")
	if w.label(w.copyNotice) != "已复制" || w.state.Notice != "原始任务错误" {
		t.Fatal("successful copy replaced task Notice")
	}
	for _, next := range []app.State{{TaskID: "next-task", Turn: 1}, {TaskID: "task", Turn: 2}} {
		w.state = app.State{TaskID: "task", Turn: 1}
		w.copyNotice = ""
		w.actions.Copy = func(string) error {
			w.state = next // Simulate a UI-thread reentrant callback.
			return nil
		}
		w.click("copy")
		if w.copyNotice != "" {
			t.Fatal("stale feedback reached a new task or turn", next)
		}
	}
}

func TestSetLocaleOnlyRepaintsWithoutChangingTaskOrInteractionState(t *testing.T) {
	w := New(Actions{})
	message := i18n.M("popup.feedback.popup_failed")
	w.state = app.State{TaskID: "task", Turn: 3, Revision: 20, Stage: app.Done, Visible: true,
		HideAt: 123456, Pinned: true, Text: "业务 result", RawText: "中文原文", Question: "模型问题？",
		Notice: message.Text(i18n.ZhCN), NoticeMessage: &message}
	w.scroll, w.maxScroll, w.target = 43, 100, 123
	w.pressed, w.pressedTask, w.pressedTurn, w.hover = "copy", "task", 3, "copy"
	w.copyNotice = "popup.copy.success"
	w.resultFollow.observe(w.state)
	w.resultFollow.target(500, 100, 600)
	before, follow := w.state, w.resultFollow
	for _, locale := range []i18n.Locale{i18n.EN, i18n.ZhCN, i18n.EN} {
		w.SetLocale(locale)
		want := before
		want.Notice = message.Text(locale)
		if !reflect.DeepEqual(w.state, want) || w.resultFollow != follow || w.scroll != 43 || w.maxScroll != 100 ||
			w.target != 123 || w.hwnd != 0 || w.pressed != "copy" || w.pressedTask != "task" || w.pressedTurn != 3 || w.hover != "copy" {
			t.Fatal("locale change reset task, time, scroll, follow, press or native window")
		}
		if got := w.label(w.copyNotice); got != i18n.M("popup.copy.success").Text(locale) {
			t.Fatal("copy feedback did not follow chrome locale", got)
		}
	}
	w.state.Notice, w.state.NoticeMessage = "模型备注无需猜测翻译", nil
	w.SetLocale(i18n.ZhCN)
	w.SetLocale(i18n.EN)
	if w.state.Notice != "模型备注无需猜测翻译" {
		t.Fatal("model-authored notice was translated")
	}
}

func TestDesktopPopupNeverActivates(t *testing.T) {
	if os.Getenv("VOICE_INPUT_DESKTOP_TEST") != "1" {
		t.Skip("requires an explicitly authorized interactive Windows desktop")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	setAwareness := user32.NewProc("SetThreadDpiAwarenessContext")
	previous, _, _ := setAwareness.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2, like the app manifest.
	defer setAwareness.Call(previous)
	instance, _, _ := getModuleHandle.Call(0)
	name, _ := windows.UTF16PtrFromString("voice_input.FocusFixture")
	defaultProc := windows.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		result, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wp, lp)
		return result
	})
	wc := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), Proc: defaultProc, Instance: instance, ClassName: name}
	if atom, _, err := registerClass.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		t.Fatalf("register fixture: %v", err)
	}
	title, _ := windows.UTF16PtrFromString("voice_input isolated focus test")
	target, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(title)),
		0x00cf0000, 80, 80, 520, 240, 0, 0, instance, 0)
	if target == 0 {
		t.Fatalf("create fixture: %v", err)
	}
	defer destroyWindow.Call(target)
	editClass, _ := windows.UTF16PtrFromString("EDIT")
	edit, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(editClass)), 0, 0x50810000,
		20, 40, 420, 32, target, 1, instance, 0)
	if edit == 0 {
		t.Fatalf("create edit: %v", err)
	}
	showWindow.Call(target, 1)
	user32.NewProc("SetForegroundWindow").Call(target)
	user32.NewProc("SetFocus").Call(edit)
	pump()
	checkFocus := func(step string) {
		t.Helper()
		pump()
		fg, _, _ := user32.NewProc("GetForegroundWindow").Call()
		focus, _, _ := user32.NewProc("GetFocus").Call()
		if fg != target || focus != edit {
			t.Fatalf("%s stole focus: foreground=%#x want=%#x focus=%#x want=%#x", step, fg, target, focus, edit)
		}
	}
	checkFocus("fixture initialization")
	var copied, closed, settings, interactions int
	type replyClick struct {
		id   string
		turn uint64
		stop bool
	}
	var replies []replyClick
	w := New(Actions{Reply: func(id string, turn uint64, stop bool) {
		replies = append(replies, replyClick{id, turn, stop})
	}, KeepOpen: func(id string) {
		interactions++
		if id == "" {
			t.Error("missing interaction task")
		}
	}, Close: func() { closed++ }, Settings: func() { settings++ }, Copy: func(id string) error {
		if id != "test-task" {
			t.Errorf("wrong copied task: %s", id)
		}
		copied++
		return nil
	}})
	defer w.Destroy()
	state := app.State{Intent: app.Dialogue, TaskID: "test-task", Revision: 1, Stage: app.Recording, Mode: "live", Visible: true,
		Hotkey: "Ctrl+Alt+R", Notice: "浮窗不得激活"}
	update := func(step string) {
		t.Helper()
		state.Revision++
		if err := w.Update(state, target); err != nil {
			t.Fatal(err)
		}
		checkFocus(step)
	}
	update("first creation and show")
	getStyle := user32.NewProc("GetWindowLongPtrW")
	style, _, _ := getStyle.Call(w.hwnd, ^uintptr(19))
	if style&(noActivate|toolWindow|0x8) != noActivate|toolWindow|0x8 {
		t.Fatalf("missing styles: %#x", style)
	}
	var bounds rect
	user32.NewProc("GetWindowRect").Call(w.hwnd, uintptr(unsafe.Pointer(&bounds)))
	monitor, _, _ := monitorFromWindow.Call(target, 2)
	info := struct {
		Size          uint32
		Monitor, Work rect
		Flags         uint32
	}{}
	info.Size = uint32(unsafe.Sizeof(info))
	getMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info)))
	if info.Work.Right-bounds.Right != w.unit(16) || info.Work.Bottom-bounds.Bottom != w.unit(16) {
		t.Fatalf("not bottom-right: popup=%+v work=%+v", bounds, info.Work)
	}
	if w.client().height() != min(w.unit(240), info.Work.height()) {
		t.Fatal("recording did not use compact geometry")
	}
	state.RawText = "测试原文"
	state.Text = strings.Repeat("最终文本保留完整内容。", 100)
	state.Stage = app.Done
	state.HideAt = time.Now().Add(30 * time.Second).UnixMilli()
	update("state update and paint")
	if w.client().height() != min(w.unit(560), info.Work.height()) {
		t.Fatal("same-task result did not resize to reading geometry")
	}
	if w.maxScroll <= 0 || w.scroll <= 0 {
		t.Fatal("long final result was not automatically brought into view")
	}
	windowProc(w.hwnd, 0x020a, uintptr(32767)<<16, 0)
	update("manual scroll stays after repeated final state")
	if w.scroll != 0 {
		t.Fatal("repeated terminal state stole manual scroll")
	}
	localeState, localeFollow, localeHWND := w.state, w.resultFollow, w.hwnd
	for _, locale := range []i18n.Locale{i18n.EN, i18n.ZhCN} {
		w.SetLocale(locale)
		checkFocus("locale-only repaint")
		if w.hwnd != localeHWND || w.scroll != 0 || w.resultFollow != localeFollow || !reflect.DeepEqual(w.state, localeState) {
			t.Fatal("language switch recreated, refollowed or changed the task")
		}
	}
	before := interactions
	windowProc(w.hwnd, 0x0200, 0, 0)
	if interactions != before+1 {
		t.Fatal("hover did not keep task open")
	}
	before = interactions
	windowProc(w.hwnd, 0x020a, uintptr(uint16(0xff88))<<16, 0)
	if interactions != before+1 {
		t.Fatal("scroll did not keep task open")
	}
	if w.scroll <= 0 {
		t.Fatal("wheel did not scroll content")
	}
	checkFocus("scroll")
	if got := windowProc(w.hwnd, 0x0021, target, 0x02010001); got != 3 {
		t.Fatalf("mouse activate=%d", got)
	}
	click := func(r rect) {
		lp := centerLParam(r)
		windowProc(w.hwnd, 0x0201, 1, lp)
		windowProc(w.hwnd, 0x0202, 0, lp)
	}
	click(w.layout().Copy)
	if copied != 1 {
		t.Fatal("copy action failed")
	}
	if w.label(w.copyNotice) != "已复制" || w.state.Notice != "浮窗不得激活" {
		t.Fatal("copy feedback overwrote the task notice")
	}
	checkFocus("copy")
	// Resize the same result while a button is pressed. The old press must
	// not reach the relocated control, and the same text must not refollow.
	beforeScroll := w.scroll
	windowProc(w.hwnd, 0x0201, 1, centerLParam(w.layout().Copy))
	state.Stage = app.LLM
	update("same-task compact resize while pressed")
	if w.pressed != "" || w.client().height() != min(w.unit(240), info.Work.height()) {
		t.Fatal("resize retained old press or old dimensions")
	}
	windowProc(w.hwnd, 0x0202, 0, centerLParam(w.layout().Copy))
	if copied != 1 {
		t.Fatal("old geometry press copied after resize")
	}
	click(w.layout().Copy)
	if copied != 2 {
		t.Fatal("processing content was no longer copyable")
	}
	checkFocus("copy while processing")
	state.Stage = app.Done
	update("same-result reading resize")
	if w.scroll != beforeScroll {
		t.Fatal("resize refollowed an already seen result")
	}
	old := state
	old.Revision--
	old.Stage, old.Text, old.RawText = app.Idle, "", ""
	if err := w.Update(old, target); err != nil || w.state.Stage != app.Done {
		t.Fatal("old snapshot was not filtered", err)
	}
	click(w.layout().Settings)
	if settings != 1 {
		t.Fatal("settings action failed")
	}
	click(w.layout().Close)
	if closed != 1 {
		t.Fatal("close action failed")
	}
	checkFocus("close action")
	state.Stage, state.Question, state.Text, state.Turn = app.Clarify, "你要查询哪里的天气？", "", 1
	state.HideAt = 0
	update("clarification question")
	if w.client().height() != min(w.unit(400), info.Work.height()) {
		t.Fatal("question did not resize to clarification geometry")
	}
	click(w.layout().Reply)
	if len(replies) != 1 || replies[0] != (replyClick{"test-task", 1, false}) {
		t.Fatal("reply-start action failed", replies)
	}
	checkFocus("reply-start click")
	state.Stage, state.Replying, state.Turn = app.Recording, true, 2
	update("reply recording")
	click(w.layout().Reply)
	if len(replies) != 2 || replies[1] != (replyClick{"test-task", 2, true}) {
		t.Fatal("reply-stop action failed", replies)
	}
	checkFocus("reply-stop click")
	state.Stage = app.LLM
	update("reply processing")
	click(w.layout().Reply)
	if len(replies) != 2 {
		t.Fatal("reply remained clickable while processing")
	}
	state.Stage, state.Replying, state.Question = app.Clarify, false, "今天还是明天？"
	update("second clarification")
	lp := centerLParam(w.layout().Reply)
	windowProc(w.hwnd, 0x0201, 1, lp)
	state.Stage, state.Replying, state.Turn = app.Recording, true, 3
	update("reply turn changed during mouse press")
	windowProc(w.hwnd, 0x0202, 0, lp)
	if len(replies) != 2 {
		t.Fatal("stale mouse press affected next turn")
	}
	state.Stage, state.Text = app.Injecting, strings.Repeat("补充后的最终正文。", 100)
	state.Sources = []search.Hit{{Title: strings.Repeat("正文下方的搜索来源。", 80), URL: "https://example.com/source"}}
	update("result before clarification is cleared")
	state.Stage, state.Question, state.Replying = app.Error, "", false
	update("copyable result after injection failure")
	if w.scroll <= 0 || w.scroll >= w.maxScroll {
		t.Fatal("final text was not followed or sources stole the result anchor")
	}
	state.Text, state.RawText = "", ""
	update("sources remain visible without a result")
	if w.maxScroll <= 0 || w.layout().Copy.width() != 0 {
		t.Fatal("sources disappeared with empty result or empty copy was still drawn")
	}
	state.Sources = nil
	state.Notice = strings.Repeat("这是必须完整可滚动阅读的错误详情。", 100)
	update("long error notice in compact body")
	if w.maxScroll <= 0 || w.client().height() != min(w.unit(240), info.Work.height()) {
		t.Fatal("long notice was truncated instead of using the compact scroll body")
	}
	state.Question, state.Stage = strings.Repeat("这是必须完整可滚动阅读的补充问题。", 100), app.Clarify
	update("long question keeps footer action accessible")
	reply := w.layout().Reply
	if w.maxScroll <= 0 || w.actionAt((reply.Left+reply.Right)/2, (reply.Top+reply.Bottom)/2) != "reply-start" {
		t.Fatal("long question lost its body or fixed reply action")
	}
	state.Visible = false
	update("hide")
	if visible, _, _ := isVisible.Call(w.hwnd); visible != 0 {
		t.Fatal("popup still visible")
	}
	state.Visible = true
	update("show after hide")
	state.TaskID = "new-task"
	state.Text, state.RawText = "", ""
	update("new task")
	if w.scroll != 0 || w.copyNotice != "" {
		t.Fatal("new task retained old UI state")
	}
	w.Destroy()
	checkFocus("destroy")
}
