package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"voice_input/internal/task"
)

func TestIntentSelectsPromptAndToolContract(t *testing.T) {
	for _, intent := range []task.Intent{task.Dictation, task.Dialogue} {
		t.Run(string(intent), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Messages []message `json:"messages"`
					Tools    []any     `json:"tools"`
				}
				if json.NewDecoder(r.Body).Decode(&payload) != nil {
					t.Error("invalid request")
					return
				}
				prompt := DictationPrompt
				if intent == task.Dialogue {
					prompt = SystemPrompt
				}
				if len(payload.Messages) != 2 || payload.Messages[0].Content != prompt || !strings.Contains(payload.Messages[1].Content, "帮我写一封请假邮件") {
					t.Error("wrong intent prompt/context")
				}
				if (len(payload.Tools) > 0) != (intent == task.Dialogue) {
					t.Error("wrong tools for intent")
				}
				if !strings.Contains(payload.Messages[1].Content, `"intent":"`+string(intent)+`"`) {
					t.Error("trusted intent missing")
				}
				text := "帮我写一封请假邮件。"
				if intent == task.Dialogue {
					text = "您好：\n\n这是固定的邮件正文 fixture。"
				}
				data, _ := json.Marshal(Result{Status: "insert", Text: text})
				wire(w, string(data), nil)
			}))
			defer server.Close()
			p := provider(server)
			search := &fakeSearch{}
			p.Search = search
			r := input()
			r.Intent = intent
			r.ASRText = "帮我写一封请假邮件"
			result, err := p.Complete(context.Background(), r)
			if err != nil || result.Status != "insert" || search.calls.Load() != 0 {
				t.Fatal(result, err)
			}
			if intent == task.Dialogue && !strings.Contains(result.Text, "\n\n") {
				t.Fatal("multiline JSON parsing lost paragraphs")
			}
		})
	}
}
func TestDictationRejectsClarificationAndToolCalls(t *testing.T) {
	for _, tools := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tools {
				wire(w, "", []toolCall{searchCall("forbidden")})
			} else {
				wire(w, `{"status":"clarify","text":"","notice":"哪个城市？"}`, nil)
			}
		}))
		p := provider(server)
		search := &fakeSearch{}
		p.Search = search
		p.MaxSearchCalls = 0
		r := input()
		r.Intent = task.Dictation
		if _, err := p.Complete(context.Background(), r); err == nil {
			t.Fatal("dictation accepted dialogue action")
		}
		if search.calls.Load() != 0 {
			t.Fatal("dictation performed search")
		}
		server.Close()
	}
}
func TestMultilineLimitCountsNormalizedNewlines(t *testing.T) {
	text := strings.Repeat("字", 7999) + "\r\n"
	data, _ := json.Marshal(Result{Status: "insert", Text: text})
	if _, err := parseResult(string(data)); err != nil {
		t.Fatal("CRLF counted twice", err)
	}
	data, _ = json.Marshal(Result{Status: "insert", Text: text + "x"})
	if _, err := parseResult(string(data)); err == nil {
		t.Fatal("multiline limit bypassed")
	}
}

func TestInvalidIntentNeverCallsProvider(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		wire(w, `{"status":"insert","text":"x","notice":""}`, nil)
	}))
	defer server.Close()
	for _, intent := range []task.Intent{"", "unknown", task.Dictation} {
		r := input()
		r.Intent = intent
		if intent == task.Dictation {
			r.Clarifications = []Clarification{{Question: "问题", Answer: "回答"}}
		}
		if _, err := provider(server).Complete(context.Background(), r); err == nil {
			t.Fatal("invalid intent/history accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request contacted provider")
	}
}
