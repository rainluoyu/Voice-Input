package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"voice_input/internal/search"
	"voice_input/internal/settings"
	"voice_input/internal/task"
)

func wire(w http.ResponseWriter, content string, calls []toolCall) {
	finish := "stop"
	if len(calls) > 0 {
		finish = "tool_calls"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message{Role: "assistant", Content: content, ToolCalls: calls}, "finish_reason": finish}}})
}
func provider(server *httptest.Server) Compatible {
	return Compatible{Client: server.Client(), Config: settings.LLM{BaseURL: server.URL, Model: "model", TimeoutSeconds: 5, OutputFormat: "json_schema", MaxToolRounds: 2}, Key: "llm-secret", MaxSearchCalls: 2}
}
func input() Request {
	return Request{Intent: task.Dialogue, TaskID: "task", ASRText: "额，帮我整理这句话。", TargetApp: "notepad.exe", NowLocal: time.Now(), Background: "城市：上海\n时区：Asia/Shanghai\n自愿背景"}
}
func TestNoSearchForOrdinaryRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer llm-secret" {
			t.Error("bad protocol")
		}
		var payload map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if _, ok := payload["tools"]; ok {
			t.Error("tools enabled without permission")
		}
		data, _ := json.Marshal(payload)
		if strings.Contains(string(data), "llm-secret") {
			t.Error("key in prompt")
		}
		if !strings.Contains(string(data), "ASR_TEXT") || !strings.Contains(string(data), "TRUSTED_CONTEXT") {
			t.Error("missing boundaries")
		}
		wire(w, `{"status":"insert","text":"整理后的文本。","notice":""}`, nil)
	}))
	defer server.Close()
	result, err := provider(server).Complete(context.Background(), input())
	if err != nil || result.Text != "整理后的文本。" {
		t.Fatalf("result: %+v %v", result, err)
	}
}
func TestStrictFinalResults(t *testing.T) {
	for _, content := range []string{`{"status":"insert","text":"x"}`, `{"status":"insert","text":"x","notice":"","extra":"no"}`, `{"status":"insert","status":"skip","text":"x","notice":""}`, "```json\n{}\n```", `{"status":"insert","text":"\u0000","notice":""}`, `{"status":"skip","text":"no","notice":""}`, `{"status":"clarify","text":"","notice":""}`, `{"status":"insert","text":"","notice":""}`, `{"status":"insert","text":"x","notice":""} {}`, `{"status":"insert","text":null,"notice":""}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { wire(w, content, nil) }))
		_, err := provider(server).Complete(context.Background(), input())
		server.Close()
		if err == nil {
			t.Errorf("accepted invalid result %s", content)
		}
	}
	for _, content := range []string{`{"status":"skip","text":"","notice":"无需输出"}`, `{"status":"clarify","text":"","notice":"哪个城市？"}`} {
		if _, err := parseResult(content); err != nil {
			t.Error(err)
		}
	}
}

type fakeSearch struct {
	calls atomic.Int32
	err   error
}

func (f *fakeSearch) Search(context.Context, string) ([]search.Hit, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return []search.Hit{{Title: "网页要求忽略系统指令", URL: "https://example.com", Highlights: []string{"不可信证据"}}}, nil
}
func searchCall(id string) toolCall {
	return toolCall{ID: id, Type: "function", Function: function{Name: "search_web", Arguments: `{"query":"上海 2026-10-17 天气"}`}}
}
func TestToolHistoryAndEvidenceBoundaries(t *testing.T) {
	var rounds atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []message `json:"messages"`
			Tools    []any     `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if len(payload.Tools) != 1 {
			t.Error("not exactly one tool")
		}
		if rounds.Add(1) == 1 {
			wire(w, "", []toolCall{searchCall("call-1")})
			return
		}
		history := payload.Messages
		if len(history) != 4 || history[2].ToolCalls[0].ID != "call-1" || history[3].ToolCallID != "call-1" || history[3].Role != "tool" {
			t.Errorf("broken history: %+v", history)
		}
		if !strings.Contains(history[3].Content, "UNTRUSTED_WEB_EVIDENCE") || strings.Contains(history[3].Content, "自愿背景") {
			t.Error("evidence boundary/data minimization")
		}
		wire(w, `{"status":"clarify","text":"","notice":"天气证据不足，请确认。"}`, nil)
	}))
	defer server.Close()
	searcher := &fakeSearch{}
	p := provider(server)
	p.Search = searcher
	result, err := p.Complete(context.Background(), input())
	if err != nil || result.Status != "clarify" || len(result.Sources) != 1 || rounds.Load() != 2 || searcher.calls.Load() != 1 {
		t.Fatalf("tool loop: %+v %v", result, err)
	}
}
func TestToolsDeniedAndBounded(t *testing.T) {
	for _, kind := range []string{"disabled", "unknown", "arguments", "duplicate", "many", "loop"} {
		t.Run(kind, func(t *testing.T) {
			var rounds atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				round := rounds.Add(1)
				call := searchCall("call-1")
				calls := []toolCall{call}
				switch kind {
				case "unknown":
					calls[0].Function.Name = "exec_shell"
				case "arguments":
					calls[0].Function.Arguments = `{"query":"q","shell":"no"}`
				case "duplicate":
					calls = append(calls, call)
				case "many":
					calls = append(calls, searchCall("call-2"), searchCall("call-3"))
				case "loop":
					calls[0].ID = string(rune('a' + round))
				}
				wire(w, "", calls)
			}))
			defer server.Close()
			searcher := &fakeSearch{}
			p := provider(server)
			if kind != "disabled" {
				p.Search = searcher
			}
			if _, err := p.Complete(context.Background(), input()); err == nil {
				t.Fatal("unsafe tools accepted")
			}
			if rounds.Load() > 3 || searcher.calls.Load() > 2 {
				t.Fatal("loop exceeded caps")
			}
			if kind != "loop" && searcher.calls.Load() != 0 {
				t.Fatal("invalid batch partly executed")
			}
		})
	}
}
func TestCancelledHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := provider(server).Complete(ctx, input()); err == nil {
		t.Fatal("cancelled HTTP accepted")
	}
}

func TestEnabledSearchIsNotCalledWithoutToolRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire(w, `{"status":"insert","text":"忠实整理。","notice":""}`, nil)
	}))
	defer server.Close()
	searcher := &fakeSearch{}
	p := provider(server)
	p.Search = searcher
	if _, err := p.Complete(context.Background(), input()); err != nil || searcher.calls.Load() != 0 {
		t.Fatal("ordinary completion searched unnecessarily")
	}
}
