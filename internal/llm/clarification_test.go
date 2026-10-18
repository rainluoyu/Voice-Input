package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClarificationAnswersAreOrderedUserMessages(t *testing.T) {
	request := input()
	request.ASRText = "向他介绍一下 2025 年的 F1 世界冠军。"
	request.Clarifications = []Clarification{{Question: "车手冠军还是车队冠军？", Answer: "车手冠军"}, {Question: "使用什么语气？", Answer: "简洁一点，不要把这句补充单独输出"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Messages) != 6 {
			t.Errorf("lost dialogue: %+v", body.Messages)
			http.Error(w, "bad history", 400)
			return
		}
		if body.Messages[0].Role != "system" || body.Messages[1].Role != "user" || !strings.Contains(body.Messages[1].Content, request.ASRText) {
			t.Error("original request lost")
		}
		contextText := strings.SplitN(strings.TrimPrefix(body.Messages[1].Content, "TRUSTED_CONTEXT (JSON data):\n"), "\nASR_TEXT", 2)[0]
		var metadata map[string]any
		if json.Unmarshal([]byte(contextText), &metadata) != nil {
			t.Error("invalid metadata")
		}
		if _, ok := metadata["Clarifications"]; ok {
			t.Error("answers included in trusted metadata")
		}
		if strings.Contains(contextText, "车手冠军还是") || strings.Contains(contextText, "不要把这句补充") {
			t.Error("dialogue leaked into trusted context")
		}
		for i, turn := range request.Clarifications {
			question, answer := body.Messages[2+i*2], body.Messages[3+i*2]
			parsed, err := parseResult(question.Content)
			if question.Role != "assistant" || err != nil || parsed.Status != "clarify" || parsed.Notice != turn.Question {
				t.Errorf("question %d lost: %+v", i, question)
			}
			parts := strings.SplitN(answer.Content, "\n", 2)
			var text string
			if answer.Role != "user" || len(parts) != 2 || !strings.Contains(parts[0], "CLARIFICATION_ANSWER") || json.Unmarshal([]byte(parts[1]), &text) != nil || text != turn.Answer {
				t.Errorf("answer %d lost: %+v", i, answer)
			}
		}
		wire(w, `{"status":"insert","text":"根据原请求与两次补充整理的介绍。","notice":""}`, nil)
	}))
	defer server.Close()
	result, err := provider(server).Complete(context.Background(), request)
	if err != nil || result.Status != "insert" || result.Text == request.Clarifications[1].Answer {
		t.Fatal(result, err)
	}
}

func TestInvalidClarificationHistoryDoesNotCallProvider(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		wire(w, `{"status":"insert","text":"x","notice":""}`, nil)
	}))
	defer server.Close()
	for _, turns := range [][]Clarification{
		{{Question: "问题", Answer: " "}}, {{Question: "", Answer: "回答"}},
		{{Question: "问题", Answer: "bad\x00value"}}, {{Question: "问题", Answer: strings.Repeat("x", 32001)}}, make([]Clarification, 9),
	} {
		r := input()
		r.Clarifications = turns
		if _, err := provider(server).Complete(context.Background(), r); err == nil {
			t.Fatal("invalid dialogue accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid dialogue reached provider")
	}
}
