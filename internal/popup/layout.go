package popup

import (
	"math"

	"voice_input/internal/app"
)

type rect struct{ Left, Top, Right, Bottom int32 }

func (r rect) width() int32  { return max(0, r.Right-r.Left) }
func (r rect) height() int32 { return max(0, r.Bottom-r.Top) }
func (r rect) contains(x, y int32) bool {
	return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom
}

// fit keeps even very small work areas legal; an unavailable control is empty.
func (r rect) fit(bounds rect) rect {
	r.Left = max(bounds.Left, min(bounds.Right, r.Left))
	r.Top = max(bounds.Top, min(bounds.Bottom, r.Top))
	r.Right = max(r.Left, min(bounds.Right, r.Right))
	r.Bottom = max(r.Top, min(bounds.Bottom, r.Bottom))
	return r
}

type displayKind uint8

const (
	compactDisplay displayKind = iota
	questionDisplay
	resultDisplay
)

func displayClass(s app.State) displayKind {
	if s.Question != "" {
		return questionDisplay
	}
	if s.Text != "" || s.RawText != "" {
		switch s.Stage {
		case app.Done, app.Error, app.Skip, app.Cancelled:
			return resultDisplay
		}
	}
	return compactDisplay
}

func resetBodyScroll(previous, next app.State) bool {
	return previous.TaskID != next.TaskID || previous.Turn != next.Turn ||
		(next.Question != "" && next.Question != previous.Question)
}

func (kind displayKind) height() int32 {
	switch kind {
	case questionDisplay:
		return 400
	case resultDisplay:
		return 560
	default:
		return 240
	}
}

func scale(n, dpi int32) int32 { return n * max(1, dpi) / 96 }

func popupPlacement(s app.State, dpi int32, work rect) rect {
	width := min(max(1, scale(400, dpi)), work.width())
	height := min(max(1, scale(displayClass(s).height(), dpi)), work.height())
	x := max(work.Left, work.Right-width-scale(16, dpi))
	y := max(work.Top, work.Bottom-height-scale(16, dpi))
	return rect{x, y, x + width, y + height}
}

// All coordinates are physical client pixels. GDI alone measures paragraphs.
// The same small geometry is used for drawing, hit testing and desktop tests.
type layout struct {
	Outer, Inner, Header, Body, Footer        rect
	Icon, Brand, Purpose, Stage, Meter, Level rect
	Settings, Close, Reply, Copy              rect
	FooterStatus, Feedback, Hint              rect
}

func popupLayout(s app.State, dpi int32, client rect) layout {
	u := func(n int32) int32 { return scale(n, dpi) }
	l := layout{Outer: client}
	edge := min(u(6), min(client.width()/2, client.height()/2))
	l.Inner = rect{client.Left + edge, client.Top + edge, client.Right - edge, client.Bottom - edge}
	inner := l.Inner
	headerHeight := min(u(96), inner.height()/2)
	footerHeight := min(u(60), inner.height()/3)
	l.Header = rect{inner.Left, inner.Top, inner.Right, inner.Top + headerHeight}
	l.Footer = rect{inner.Left, inner.Bottom - footerHeight, inner.Right, inner.Bottom}
	l.Body = rect{inner.Left + u(14), l.Header.Bottom + u(8), inner.Right - u(14), l.Footer.Top - u(8)}.fit(inner)
	// The body must never extend into the footer on a tiny display.
	l.Body.Top = min(l.Body.Top, l.Footer.Top)
	l.Body.Bottom = max(l.Body.Top, min(l.Body.Bottom, l.Footer.Top))
	box := func(x, y, width, height int32, bounds rect) rect {
		return rect{x, y, x + max(0, width), y + max(0, height)}.fit(bounds)
	}
	left, right, top := inner.Left+u(14), inner.Right-u(14), inner.Top
	l.Close = box(right-u(28), top+u(12), u(28), u(30), l.Header)
	l.Settings = box(l.Close.Left-u(96), top+u(12), u(88), u(30), l.Header)
	l.Icon = box(left, top+u(14), u(28), u(28), l.Header)
	l.Brand = box(left+u(40), top+u(10), l.Settings.Left-u(8)-left-u(40), u(22), l.Header)
	l.Purpose = box(l.Brand.Left, top+u(32), l.Brand.width(), u(18), l.Header)
	l.Stage = box(left, top+u(54), right-left, u(26), l.Header)
	if s.Stage == app.Recording {
		l.Stage.Right = max(l.Stage.Left, l.Stage.Right-u(112))
	}
	l.Meter = box(right-u(96), top+u(54), u(96), u(24), l.Header)
	l.Level = box(left, top+u(78), right-left, u(18), l.Header)
	l.FooterStatus = box(left, l.Footer.Top+u(2), max(0, right-left-u(104)), u(18), l.Footer)
	l.Feedback = box(max(left, right-u(100)), l.Footer.Top+u(2), min(u(100), right-left), u(18), l.Footer)
	buttonsTop := l.Footer.Bottom - u(34)
	buttonsRight := right
	if s.Text != "" || s.RawText != "" {
		l.Copy = box(max(left, right-u(76)), buttonsTop, min(u(76), right-left), u(30), l.Footer)
		buttonsRight = l.Copy.Left - u(8)
	}
	if s.Question != "" {
		l.Reply = box(left, buttonsTop, min(u(160), max(0, buttonsRight-left)), u(30), l.Footer)
	} else {
		l.Hint = box(left, buttonsTop+u(6), max(0, buttonsRight-left), u(20), l.Footer)
	}
	return l
}

func (l layout) actionAt(s app.State, x, y int32) string {
	switch {
	case l.Close.contains(x, y):
		return "close"
	case l.Settings.contains(x, y):
		return "settings"
	case l.Reply.contains(x, y):
		return replyAction(s)
	case l.Copy.contains(x, y):
		return "copy"
	}
	return ""
}

// Match the Web meter: fixed symmetric geometry, driven only by real level.
// These are DIP heights, not synthesized frequencies or a time-based animation.
func meterBars(level float64) (bars [9]int32) {
	if math.IsNaN(level) || math.IsInf(level, 0) {
		level = 0
	}
	level = max(0, min(1, level*8))
	weights := [9]float64{.28, .5, .72, .9, 1, .9, .72, .5, .28}
	for i, weight := range weights {
		bars[i] = int32(math.Round(2 + 22*level*weight))
	}
	return bars
}
