package popup

import (
	"testing"

	"voice_input/internal/app"
)

func TestFinalResultFollowsOnlyOnceAfterQuestionClears(t *testing.T) {
	var follow resultFollow
	s := app.State{TaskID: "task", Turn: 2, Visible: true, Stage: app.Injecting, Text: "最终正文", Question: "哪个城市？"}
	follow.observe(s)
	if _, ok := follow.target(1200, 350, 2000); ok {
		t.Fatal("followed before final layout")
	}
	s.Stage, s.Question = app.Done, ""
	follow.observe(s)
	if target, ok := follow.target(1200, 350, 2000); !ok || target != 850 {
		t.Fatal(target, ok)
	}
	// A long source list extends the document, but must not become the target.
	for range 4 {
		s.Revision++
		s.Pinned = true
		follow.observe(s)
		if _, ok := follow.target(1200, 350, 9000); ok {
			t.Fatal("repeated result stole manual scroll")
		}
	}
}

func TestResultFollowCancellationAndNewIdentity(t *testing.T) {
	var follow resultFollow
	s := app.State{TaskID: "task", Turn: 1, Visible: true, Stage: app.Error, Text: "可复制正文"}
	follow.observe(s)
	follow.cancel()
	follow.observe(s)
	if _, ok := follow.target(500, 300, 800); ok {
		t.Fatal("manual scroll did not cancel delayed follow")
	}
	s.Turn++
	follow.observe(s)
	if target, ok := follow.target(500, 300, 800); !ok || target != 200 {
		t.Fatal("new turn not followed", target, ok)
	}
	s.TaskID = "new-task"
	follow.observe(s)
	if _, ok := follow.target(500, 300, 800); !ok {
		t.Fatal("new task not followed")
	}
	s.Text = "修改后的正文"
	follow.observe(s)
	if target, ok := follow.target(50, 300, 800); !ok || target != 0 {
		t.Fatal("short result not clamped", target, ok)
	}
}

func TestResultFollowUsesFinalBodyViewportAndNeverRefollowsResize(t *testing.T) {
	for _, dpi := range []int32{96, 144, 192} {
		var follow resultFollow
		s := app.State{TaskID: "task", Turn: 2, Visible: true, Stage: app.Error, Text: "完整正文", Question: "补充？"}
		follow.observe(s)
		// Clear the question and resize before measuring the final body.
		s.Question = ""
		follow.observe(s)
		bounds := popupPlacement(s, dpi, rect{0, 0, 1920, 1080})
		l := popupLayout(s, dpi, rect{0, 0, bounds.width(), bounds.height()})
		bottom, sourceTail := scale(1300, dpi), scale(9000, dpi)
		want := bottom - l.Body.height()
		if got, ok := follow.target(bottom, l.Body.height(), sourceTail-l.Body.height()); !ok || got != want {
			t.Fatal("follow did not use resized body or followed sources", dpi, got, want, ok)
		}
		// Work-area/DPI/category changes must not make the same result pending.
		for _, height := range []int32{200, 400, 560, 220} {
			s.Question = "仍可查看的问题"
			s.Revision++
			follow.observe(s)
			l = popupLayout(s, dpi, rect{0, 0, scale(400, dpi), scale(height, dpi)})
			if _, ok := follow.target(bottom, l.Body.height(), sourceTail); ok {
				t.Fatal("resize stole manually selected viewport", dpi, height)
			}
		}
	}
}

func TestResultFollowIgnoresHiddenAndNonFinalStates(t *testing.T) {
	for _, stage := range []app.Stage{app.Idle, app.Recording, app.ASR, app.LLM, app.Search, app.Injecting, app.Clarify, app.Skip, app.Cancelled} {
		var follow resultFollow
		follow.observe(app.State{TaskID: "task", Turn: 1, Visible: true, Stage: stage, Text: "旧正文"})
		if _, ok := follow.target(1000, 300, 900); ok {
			t.Fatalf("followed during %s", stage)
		}
	}
	var follow resultFollow
	s := app.State{TaskID: "task", Turn: 1, Stage: app.Done, Text: "正文"}
	follow.observe(s)
	if _, ok := follow.target(1000, 300, 900); ok {
		t.Fatal("hidden result followed")
	}
	s.Visible = true
	follow.observe(s)
	if target, ok := follow.target(1000, 300, 600); !ok || target != 600 {
		t.Fatal("visible snapshot not followed or not clamped", target, ok)
	}
	s.Text = ""
	follow.observe(s)
	if _, ok := follow.target(1000, 300, 900); ok {
		t.Fatal("empty result followed")
	}
}
