package popup

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"voice_input/internal/app"
	"voice_input/internal/i18n"
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

func TestBilingualStageAndPurposeLabels(t *testing.T) {
	for _, tc := range []struct {
		state  app.State
		en, zh string
	}{
		{app.State{Stage: app.Idle}, "Ready", "准备就绪"},
		{app.State{Stage: app.Recording}, "Recording", "正在录音"},
		{app.State{Stage: app.ASR}, "Transcribing", "识别语音"},
		{app.State{Stage: app.LLM, Intent: app.Dictation}, "Polishing text", "正在润色文本"},
		{app.State{Stage: app.LLM, Intent: app.Dialogue}, "Generating text", "生成所需文本"},
		{app.State{Stage: app.Search}, "Checking facts", "查证事实"},
		{app.State{Stage: app.Injecting}, "Typing", "正在输入"},
		{app.State{Stage: app.Done}, "Text entered", "已输入"},
		{app.State{Stage: app.Skip}, "No output needed", "无需输出"},
		{app.State{Stage: app.Clarify}, "More information needed", "需要补充信息"},
		{app.State{Stage: app.Error}, "Input incomplete", "输入未完成"},
		{app.State{Stage: app.Cancelled}, "Cancelled", "已取消"},
		{app.State{Stage: app.Recording, Replying: true}, "Recording reply", "正在录制补充"},
		{app.State{Stage: app.ASR, Replying: true}, "Transcribing reply", "识别补充语音"},
	} {
		for locale, want := range map[i18n.Locale]string{i18n.EN: tc.en, i18n.ZhCN: tc.zh} {
			if got := stageLabelForLocale(tc.state, locale); got != want {
				t.Fatalf("stage=%s locale=%s got=%q want=%q", tc.state.Stage, locale, got, want)
			}
		}
	}
	for _, tc := range []struct {
		intent app.Intent
		en, zh string
	}{
		{app.Dictation, "Voice polish", "语音润色"},
		{app.Dialogue, "AI dialogue", "AI 对话"},
	} {
		for locale, want := range map[i18n.Locale]string{i18n.EN: tc.en, i18n.ZhCN: tc.zh} {
			s := app.State{Intent: tc.intent}
			if got := purposeLabelForLocale(s, locale); got != want {
				t.Fatal(locale, got, want)
			}
			s.Mode = "asr_mock_llm"
			want += i18n.M("popup.mode.asr_passthrough").Text(locale)
			if got := purposeLabelForLocale(s, locale); got != want {
				t.Fatal(locale, got, want)
			}
		}
	}
}

func TestLocalizedFooterKeepsTimeAndReplyActions(t *testing.T) {
	now := time.UnixMilli(10000)
	for _, tc := range []struct {
		state  app.State
		en, zh string
	}{
		{app.State{HideAt: 10001}, "Hides in 1 s", "1 秒后隐藏"},
		{app.State{HideAt: 9000}, "Hides in 0 s", "0 秒后隐藏"},
		{app.State{HideAt: 10001, Pinned: true}, "Stays visible", "保持显示"},
		{app.State{Stage: app.Clarify, Hotkey: "Ctrl+Alt+R", Pinned: true}, "Ctrl+Alt+R to reply", "Ctrl+Alt+R 开始补充"},
		{app.State{Stage: app.Recording, Hotkey: "Ctrl+Alt+R"}, "Ctrl+Alt+R to stop recording", "Ctrl+Alt+R 结束录音"},
		{app.State{Stage: app.Recording, Hotkey: "Ctrl+Alt+R", Replying: true}, "Ctrl+Alt+R to stop reply", "Ctrl+Alt+R 结束补充"},
	} {
		state := tc.state
		state.TaskID, state.Turn, state.Revision = "task", 3, 27
		before, action := state, replyAction(state)
		for locale, want := range map[i18n.Locale]string{i18n.EN: tc.en, i18n.ZhCN: tc.zh} {
			if got := footerForLocale(state, now, locale); got != want {
				t.Fatal(locale, got, want)
			}
			if !reflect.DeepEqual(state, before) || replyAction(state) != action {
				t.Fatal("localized model changed task, time, or reply behavior")
			}
		}
	}
}

func TestLocaleRenderingKeepsSourceAndResultIdentity(t *testing.T) {
	s := app.State{TaskID: "task", Turn: 2, Revision: 10, Stage: app.Done, Visible: true,
		Text: "Do not translate 业务文本", RawText: "原始语音", Question: "模型问题？", Notice: "模型备注",
		Sources: []search.Hit{{Title: "中文 source", URL: "https://example.com/中文", PublishedDate: "2026-10-17"}}}
	before := s
	before.Sources = append([]search.Hit{}, s.Sources...)
	follow := resultFollow{}
	follow.observe(s)
	follow.target(500, 100, 600)
	for _, locale := range []i18n.Locale{i18n.EN, i18n.ZhCN, i18n.EN} {
		stageLabelForLocale(s, locale)
		purposeLabelForLocale(s, locale)
		footerForLocale(s, time.UnixMilli(10000), locale)
		got := sourceTextForLocale(s, locale)
		for _, value := range []string{s.Sources[0].Title, s.Sources[0].URL, s.Sources[0].PublishedDate} {
			if !strings.Contains(got, value) {
				t.Fatal("source evidence changed", locale, got)
			}
		}
		follow.observe(s)
		if _, pending := follow.target(500, 100, 600); pending || resetBodyScroll(before, s) {
			t.Fatal("locale rendering repeated follow or reset scroll")
		}
		if !reflect.DeepEqual(s, before) {
			t.Fatal("locale rendering mutated business content")
		}
	}
}
