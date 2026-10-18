package popup

import (
	"math"
	"testing"

	"voice_input/internal/app"
	"voice_input/internal/i18n"
)

func TestDisplayClassStageMatrix(t *testing.T) {
	stages := []app.Stage{app.Idle, app.Recording, app.ASR, app.LLM, app.Search, app.Injecting,
		app.Done, app.Error, app.Skip, app.Cancelled, app.Clarify}
	for _, stage := range stages {
		for _, content := range []struct{ text, raw string }{{}, {text: "正文"}, {raw: "原文"}, {text: "正文", raw: "原文"}} {
			s := app.State{Stage: stage, Text: content.text, RawText: content.raw}
			want := compactDisplay
			if (stage == app.Done || stage == app.Error || stage == app.Skip || stage == app.Cancelled) && (s.Text != "" || s.RawText != "") {
				want = resultDisplay
			}
			if got := displayClass(s); got != want {
				t.Fatalf("stage=%s text=%q raw=%q: got %v want %v", stage, s.Text, s.RawText, got, want)
			}
			s.Question, s.Replying = "需要哪个日期？", true
			if got := displayClass(s); got != questionDisplay || got.height() != 400 {
				t.Fatalf("question lost during %s: %v", stage, got)
			}
		}
	}
	if compactDisplay.height() != 240 || resultDisplay.height() != 560 {
		t.Fatal("display heights changed")
	}
}

func TestPlacementAndLayoutAcrossDPIAndWorkAreas(t *testing.T) {
	states := []app.State{
		{Stage: app.Recording},
		{Intent: app.Dialogue, Stage: app.Clarify, Question: "补充？", RawText: "原文"},
		{Stage: app.Done, Text: "正文"},
	}
	for _, dpi := range []int32{96, 120, 144, 192, 240} {
		for _, work := range []rect{{0, 0, 1920, 1040}, {-1920, -1080, 0, -40}, {-500, -100, -260, 80}, {0, 0, 40, 24}, {-1, -1, 0, 0}} {
			for _, s := range states {
				bounds := popupPlacement(s, dpi, work)
				if bounds.width() <= 0 || bounds.height() <= 0 || bounds.fit(work) != bounds {
					t.Fatalf("illegal placement dpi=%d work=%+v bounds=%+v", dpi, work, bounds)
				}
				if bounds.width() != min(scale(400, dpi), work.width()) || bounds.height() != min(scale(displayClass(s).height(), dpi), work.height()) {
					t.Fatal("wrong clamped size", dpi, work, bounds)
				}
				if work.width() >= bounds.width()+scale(16, dpi) && work.Right-bounds.Right != scale(16, dpi) {
					t.Fatal("right anchor changed", bounds)
				}
				if work.height() >= bounds.height()+scale(16, dpi) && work.Bottom-bounds.Bottom != scale(16, dpi) {
					t.Fatal("bottom anchor changed", bounds)
				}
				l := popupLayout(s, dpi, rect{0, 0, bounds.width(), bounds.height()})
				for name, r := range map[string]rect{
					"inner": l.Inner, "header": l.Header, "body": l.Body, "footer": l.Footer,
					"icon": l.Icon, "brand": l.Brand, "purpose": l.Purpose, "stage": l.Stage,
					"meter": l.Meter, "level": l.Level, "settings": l.Settings, "close": l.Close,
					"reply": l.Reply, "copy": l.Copy, "footer status": l.FooterStatus, "feedback": l.Feedback, "hint": l.Hint,
				} {
					if r.Right < r.Left || r.Bottom < r.Top || (r.width() > 0 && r.height() > 0 && r.fit(l.Outer) != r) {
						t.Fatalf("illegal %s dpi=%d work=%+v: %+v", name, dpi, work, r)
					}
				}
				if l.Header.Bottom > l.Body.Top || l.Body.Bottom > l.Footer.Top {
					t.Fatalf("regions overlap: %+v", l)
				}
				actions := []rect{l.Settings, l.Close, l.Reply, l.Copy}
				for i, a := range actions {
					for _, b := range actions[i+1:] {
						if overlaps(a, b) {
							t.Fatalf("buttons overlap dpi=%d work=%+v: %+v %+v", dpi, work, a, b)
						}
					}
				}
			}
		}
	}
}

func overlaps(a, b rect) bool {
	return a.width() > 0 && a.height() > 0 && b.width() > 0 && b.height() > 0 &&
		a.Left < b.Right && b.Left < a.Right && a.Top < b.Bottom && b.Top < a.Bottom
}

func TestLayoutActionsUseDrawnRects(t *testing.T) {
	states := []app.State{
		{Intent: app.Dialogue, Stage: app.Clarify, Question: "日期？"},
		{Intent: app.Dialogue, Stage: app.Recording, Question: "日期？", Replying: true, RawText: "可复制原文"},
		{Intent: app.Dialogue, Stage: app.ASR, Question: "日期？", RawText: "可复制原文"},
		{Stage: app.LLM, Text: "处理中已有正文"},
		{Stage: app.Error},
	}
	for _, s := range states {
		l := popupLayout(s, 144, rect{0, 0, 600, scale(displayClass(s).height(), 144)})
		for _, tc := range []struct {
			r    rect
			want string
		}{{l.Close, "close"}, {l.Settings, "settings"}, {l.Reply, replyAction(s)}, {l.Copy, "copy"}} {
			if tc.r.width() == 0 || tc.r.height() == 0 {
				continue
			}
			x, y := (tc.r.Left+tc.r.Right)/2, (tc.r.Top+tc.r.Bottom)/2
			if got := l.actionAt(s, x, y); got != tc.want {
				t.Fatalf("stage=%s rect=%+v: got %q want %q", s.Stage, tc.r, got, tc.want)
			}
		}
		if (l.Copy.width() > 0) != (s.Text != "" || s.RawText != "") {
			t.Fatal("copy visibility depends on stage instead of content", s)
		}
		if (l.Reply.width() > 0) != (s.Question != "") {
			t.Fatal("question action not retained in footer", s)
		}
	}
}

func TestQuestionResetsBodyWithoutRefollowingResize(t *testing.T) {
	previous := app.State{TaskID: "task", Turn: 2, Stage: app.LLM, Question: "原来的问题？"}
	for _, tc := range []struct {
		name  string
		edit  func(*app.State)
		reset bool
	}{
		{"new task", func(s *app.State) { s.TaskID = "new-task" }, true},
		{"new turn", func(s *app.State) { s.Turn++ }, true},
		{"new question", func(s *app.State) { s.Stage = app.Clarify; s.Question = "下一条问题？" }, true},
		{"question cleared for result", func(s *app.State) { s.Stage = app.Done; s.Question = ""; s.Text = "正文" }, false},
		{"same question", func(s *app.State) { s.Stage = app.Clarify }, false},
		{"pin and revision", func(s *app.State) { s.Revision++; s.Pinned = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := previous
			tc.edit(&next)
			if got := resetBodyScroll(previous, next); got != tc.reset {
				t.Fatalf("reset=%v want=%v", got, tc.reset)
			}
		})
	}
}

func TestBilingualLayoutKeepsGeometryAndLargerSettings(t *testing.T) {
	for _, dpi := range []int32{96, 120, 144, 192, 240} {
		for _, s := range []app.State{
			{Stage: app.Recording},
			{Stage: app.Clarify, Intent: app.Dialogue, Question: "业务问题"},
			{Stage: app.Done, Text: "Business result"},
		} {
			bounds := popupPlacement(s, dpi, rect{0, 0, 1920, 1080})
			client := rect{0, 0, bounds.width(), bounds.height()}
			baseline := popupLayout(s, dpi, client)
			if baseline.Settings.width() != scale(88, dpi) || baseline.Settings.height() != scale(30, dpi) {
				t.Fatal("Settings must gain 44 DIP for its English label", baseline.Settings)
			}
			for _, locale := range []i18n.Locale{i18n.EN, i18n.ZhCN} {
				if label := i18n.M("popup.button.settings").Text(locale); label == "" || label == "popup.button.settings" {
					t.Fatal("missing Settings label", locale)
				}
				stageLabelForLocale(s, locale)
				if l := popupLayout(s, dpi, client); l != baseline || popupPlacement(s, dpi, rect{0, 0, 1920, 1080}) != bounds {
					t.Fatal("language changed fixed geometry or anchoring", locale)
				}
			}
		}
	}
}

func TestMeterBarsClampAndMatchWeb(t *testing.T) {
	zero := [9]int32{2, 2, 2, 2, 2, 2, 2, 2, 2}
	full := [9]int32{8, 13, 18, 22, 24, 22, 18, 13, 8}
	for _, level := range []float64{-100, -.1, 0, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := meterBars(level); got != zero {
			t.Fatal("invalid level was not silent", level, got)
		}
	}
	for _, level := range []float64{.125, 1, 100} {
		if got := meterBars(level); got != full {
			t.Fatal("gain or clamp differs from Web", level, got)
		}
	}
	previous := zero
	for step := 0; step <= 125; step++ {
		bars := meterBars(float64(step) / 1000)
		for i, height := range bars {
			if height < previous[i] || height < 2 || height > 24 || height != bars[8-i] {
				t.Fatal("non-monotonic, asymmetric or out-of-range bars", bars)
			}
		}
		previous = bars
	}
}
