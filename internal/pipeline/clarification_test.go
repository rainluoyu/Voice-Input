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
	"voice_input/internal/asr"
	"voice_input/internal/llm"
	"voice_input/internal/settings"
)

func TestHTTPClarificationRoundTrip(t *testing.T) {
	var asrCalls, llmCalls, factoryCalls atomic.Int32
	original := "向他介绍一下 2025 年的 F1 世界冠军。"
	question := "你指的是车手冠军，还是车队冠军？"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asr":
			text := original
			if asrCalls.Add(1) > 1 {
				text = "车手冠军"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
		case "/v1/chat/completions":
			n := llmCalls.Add(1)
			var body struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			content := `{"status":"clarify","text":"","notice":"你指的是车手冠军，还是车队冠军？"}`
			if n == 2 {
				if len(body.Messages) != 4 || body.Messages[2].Role != "assistant" || body.Messages[3].Role != "user" || !strings.Contains(body.Messages[1].Content, original) || !strings.Contains(body.Messages[2].Content, question) || !strings.Contains(body.Messages[3].Content, "车手冠军") {
					t.Error("follow-up request lost original context")
				}
				content = `{"status":"insert","text":"根据原请求和补充生成的介绍。","notice":""}`
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cfg := settings.Default()
	cfg.Mode = "live"
	cfg.ASR.Endpoint = server.URL + "/asr"
	cfg.ASR.Model = "asr"
	cfg.LLM.BaseURL = server.URL + "/v1"
	cfg.LLM.Model = "llm"
	injector := &fakeInjector{}
	controller := app.NewWithFactory(func(ctx context.Context, id string, intent app.Intent, notify func(app.Stage, float64)) (app.Processor, app.TaskOptions, error) {
		factoryCalls.Add(1)
		session := &Session{Recorder: fakeRecorder{}, ASR: asr.Compatible{Client: server.Client(), Config: cfg.ASR, Key: "asr-key"}, LLM: llm.Compatible{Client: server.Client(), Config: cfg.LLM, Key: "llm-key"}, Config: cfg, TaskID: id, Intent: intent, Notify: notify}
		return session, app.TaskOptions{Mode: "live", Hotkey: "Ctrl+Alt+R", Timeout: time.Second}, nil
	}, injector, 30*time.Second)
	defer controller.Close()
	wait := func(stage app.Stage) app.State {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			s := controller.Snapshot()
			if s.Stage == stage {
				return s
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("stage %s not reached: %+v", stage, controller.Snapshot())
		return app.State{}
	}
	controller.Toggle(app.Dialogue)
	controller.Toggle(app.Dialogue)
	pending := wait(app.Clarify)
	if pending.Question != question || pending.HideAt != 0 || injector.count.Load() != 0 {
		t.Fatal(pending)
	}
	controller.Reply(pending.TaskID, pending.Turn, false)
	recording := controller.Snapshot()
	controller.Reply(recording.TaskID, recording.Turn, true)
	end := wait(app.Done)
	if end.TaskID != pending.TaskID || end.Turn != 2 || end.Text != "根据原请求和补充生成的介绍。" || !strings.Contains(end.RawText, "补充 1：车手冠军") || injector.count.Load() != 1 || asrCalls.Load() != 2 || llmCalls.Load() != 2 || factoryCalls.Load() != 2 {
		t.Fatalf("roundtrip failed: %+v", end)
	}
}
