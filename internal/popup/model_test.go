package popup

import (
	"strings"
	"testing"
	"time"

	"voice_input/internal/app"
	"voice_input/internal/search"
)

func TestFooterAndLabels(t *testing.T) {
	now := time.UnixMilli(10000)
	if got := footer(app.State{Pinned: true, HideAt: 10001}, now); got != "保持显示" || strings.Contains(got, "秒") {
		t.Fatal(got)
	}
	if got := footer(app.State{HideAt: 10001}, now); !strings.HasPrefix(got, "1 秒") {
		t.Fatalf("countdown rounded down: %s", got)
	}
	if got := footer(app.State{HideAt: 9000}, now); !strings.HasPrefix(got, "0 秒") {
		t.Fatalf("negative countdown: %s", got)
	}
	if got := footer(app.State{Stage: app.Recording, Hotkey: "Ctrl+Alt+R"}, now); !strings.Contains(got, "Ctrl+Alt+R") {
		t.Fatal(got)
	}
	if got := stageLabel(app.State{Stage: app.Recording, Mode: "live"}); got != "正在录音" {
		t.Fatal(got)
	}
	for _, stage := range []app.Stage{app.Idle, app.Recording, app.ASR, app.LLM, app.Search, app.Injecting,
		app.Done, app.Skip, app.Clarify, app.Error, app.Cancelled} {
		if stageLabel(app.State{Stage: stage}) == "" {
			t.Fatalf("missing label: %s", stage)
		}
	}
}

func TestReplyLabelsAndActions(t *testing.T) {
	if replyAction(app.State{Intent: app.Dictation, Stage: app.Clarify}) != "" {
		t.Fatal("dictation exposed reply action")
	}
	if stageLabel(app.State{Intent: app.Dictation, Stage: app.LLM}) != "正在润色文本" || stageLabel(app.State{Intent: app.Dialogue, Stage: app.LLM}) != "生成所需文本" {
		t.Fatal("intent labels indistinguishable")
	}
	now := time.Now()
	waiting := app.State{Intent: app.Dialogue, Stage: app.Clarify, Question: "哪个城市？", Hotkey: "Ctrl+Alt+R", Pinned: true}
	if replyAction(waiting) != "reply-start" || !strings.Contains(footer(waiting, now), "开始补充") || strings.Contains(footer(waiting, now), "秒") {
		t.Fatal("missing waiting action")
	}
	recording := app.State{Intent: app.Dialogue, Stage: app.Recording, Replying: true, Hotkey: "Ctrl+Alt+R"}
	if replyAction(recording) != "reply-stop" || stageLabel(recording) != "正在录制补充" || !strings.Contains(footer(recording, now), "结束补充") {
		t.Fatal("missing recording action")
	}
	for _, stage := range []app.Stage{app.ASR, app.LLM, app.Done, app.Cancelled} {
		if replyAction(app.State{Stage: stage, Replying: true}) != "" {
			t.Fatalf("reply enabled during %s", stage)
		}
	}
}

func TestSourcesRetainEvidence(t *testing.T) {
	s := app.State{Sources: []search.Hit{{Title: "证据", URL: "https://example.com/", PublishedDate: "2026-10-17"}}}
	if got := sourceText(s); got != "证据（2026-10-17）\nhttps://example.com/" {
		t.Fatal(got)
	}
}
