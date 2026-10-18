package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"voice_input/internal/app"
	"voice_input/internal/i18n"
	"voice_input/internal/llm"
)

func TestFactorySnapshotsLocaleWithoutChangingASRLanguage(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.EN, i18n.ZhCN} {
		ctx, cancel := context.WithCancel(context.Background())
		cfg := liveConfig()
		cfg.UILanguage = string(locale)
		cfg.ASR.Language = "zh"
		processor, options, err := New(ctx, "task", app.Dialogue, cfg, keyVault{"asr": "key", "llm": "key"}, nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		session := processor.(*Session)
		cfg.UILanguage = "other"
		cfg.ASR.Language = "en"
		compatible := session.LLM.(llm.Compatible)
		if options.Locale != locale || session.Locale != locale || compatible.Locale != locale || session.Config.ASR.Language != "zh" || session.Config.UILanguage != string(locale) {
			t.Fatal("snapshot or ASR language changed")
		}
		cancel()
	}
}

func TestClarificationFactoryKeepsOriginalLocaleOnWire(t *testing.T) {
	var modelCalls, asrCalls atomic.Int32
	var uiLanguage atomic.Value
	uiLanguage.Store("en")
	var seenLocales atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asr":
			call := asrCalls.Add(1)
			if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("language") != "zh" {
				t.Error("UI locale changed ASR language", err)
			}
			text := "原始中文请求"
			if call > 1 {
				text = "上海"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
		case "/v1/chat/completions":
			call := modelCalls.Add(1)
			var body struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) < 2 {
				t.Error("invalid completion", err)
				w.WriteHeader(400)
				return
			}
			defaults := llm.DefaultPromptsForLocale(i18n.EN)
			if !strings.Contains(body.Messages[0].Content, defaults.Dialogue) || strings.Contains(body.Messages[0].Content, llm.DefaultPrompts().Dialogue) {
				t.Error("same-task clarification changed prompt language")
			}
			content := `{"status":"clarify","text":"","notice":"哪个城市？"}`
			if call > 1 {
				content = `{"status":"skip","text":"","notice":"模型原文不翻译"}`
				if len(body.Messages) != 4 {
					t.Error("clarification history lost")
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	controller := app.NewWithFactory(func(ctx context.Context, id string, intent app.Intent, notify func(app.Stage, float64)) (app.Processor, app.TaskOptions, error) {
		cfg := liveConfig()
		cfg.UILanguage = uiLanguage.Load().(string)
		cfg.ASR.Endpoint = server.URL + "/asr"
		cfg.LLM.BaseURL = server.URL + "/v1"
		cfg.ASR.Language = "zh"
		processor, options, err := New(ctx, id, intent, cfg, keyVault{"asr": "key", "llm": "key"}, notify)
		if err == nil {
			session := processor.(*Session)
			session.Recorder = fakeRecorder{}
			if session.Locale == i18n.EN && options.Locale == i18n.EN && session.LLM.(llm.Compatible).Locale == i18n.EN {
				seenLocales.Add(1)
			}
		}
		return processor, options, err
	}, &fakeInjector{}, time.Hour)
	defer controller.Close()
	wait := func(stage app.Stage, turn uint64) app.State {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			state := controller.Snapshot()
			if state.Stage == stage && state.Turn == turn {
				return state
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("state not reached: %+v", controller.Snapshot())
		return app.State{}
	}
	controller.Toggle(app.Dialogue)
	controller.Toggle(app.Dialogue)
	first := wait(app.Clarify, 1)
	uiLanguage.Store("zh-CN")
	controller.Reply(first.TaskID, first.Turn, false)
	controller.Toggle(app.Dialogue)
	end := wait(app.Skip, 2)
	if seenLocales.Load() != 2 || modelCalls.Load() != 2 || asrCalls.Load() != 2 || end.TaskID != first.TaskID {
		t.Fatal("task locale or identity changed")
	}
	copy, err := controller.CopyText(end.TaskID)
	if err != nil || copy != end.RawText || !strings.Contains(copy, "Original speech: 原始中文请求") || !strings.Contains(copy, "Question 1: 哪个城市？\nSupplement 1: 上海") || end.Notice != "模型原文不翻译" || end.NoticeMessage != nil {
		t.Fatal("display/copy or model data changed", end, err)
	}
}
