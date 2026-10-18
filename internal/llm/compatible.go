package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"voice_input/internal/netapi"
	"voice_input/internal/search"
	"voice_input/internal/settings"
	"voice_input/internal/task"
)

type Request struct {
	Intent         task.Intent     `json:"intent"`
	TaskID         string          `json:"task_id"`
	ASRText        string          `json:"asr_text"`
	TargetApp      string          `json:"target_app"`
	NowLocal       time.Time       `json:"now_local"`
	Background     string          `json:"background"`
	Clarifications []Clarification `json:"-"`
}

// Clarifications are conversational user/assistant messages, never trusted metadata.
type Clarification struct {
	Question string
	Answer   string
}
type Result struct {
	Status  string       `json:"status"`
	Text    string       `json:"text"`
	Notice  string       `json:"notice"`
	Sources []search.Hit `json:"-"`
}
type Provider interface {
	Complete(context.Context, Request) (Result, error)
}
type Compatible struct {
	Client         *http.Client
	Config         settings.LLM
	Key            string
	Search         search.Provider
	MaxSearchCalls int
	OnStage        func(string)
}
type function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type toolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function function `json:"function"`
}
type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

func parseResult(data string) (Result, error) {
	var fields struct {
		Status *string `json:"status"`
		Text   *string `json:"text"`
		Notice *string `json:"notice"`
	}
	if netapi.StrictJSON([]byte(data), &fields) != nil || fields.Status == nil || fields.Text == nil || fields.Notice == nil {
		return Result{}, netapi.Fail("llm", "response")
	}
	r := Result{Status: *fields.Status, Text: *fields.Text, Notice: *fields.Notice}
	if strings.ContainsRune(r.Text, 0) || strings.ContainsRune(r.Notice, 0) || utf8.RuneCountInString(strings.ReplaceAll(r.Text, "\r\n", "\n")) > 8000 || utf8.RuneCountInString(r.Notice) > 500 {
		return Result{}, netapi.Fail("llm", "response")
	}
	switch r.Status {
	case "insert":
		if strings.TrimSpace(r.Text) == "" {
			return Result{}, netapi.Fail("llm", "response")
		}
	case "skip":
		if r.Text != "" {
			return Result{}, netapi.Fail("llm", "response")
		}
	case "clarify":
		if r.Text != "" || strings.TrimSpace(r.Notice) == "" {
			return Result{}, netapi.Fail("llm", "response")
		}
	default:
		return Result{}, netapi.Fail("llm", "response")
	}
	return r, nil
}
func schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"status": map[string]any{"type": "string", "enum": []string{"insert", "skip", "clarify"}}, "text": map[string]string{"type": "string"}, "notice": map[string]string{"type": "string"}}, "required": []string{"status", "text", "notice"}, "additionalProperties": false}
}
func (p Compatible) Complete(ctx context.Context, input Request) (Result, error) {
	if !input.Intent.Valid() || (input.Intent == task.Dictation && len(input.Clarifications) != 0) {
		return Result{}, netapi.Fail("llm", "request")
	}
	if p.Config.MaxToolRounds < 0 || p.Config.MaxToolRounds > 8 {
		return Result{}, netapi.Fail("llm", "config")
	}
	for _, business := range []string{p.Config.DictationPrompt, p.Config.DialoguePrompt} {
		if !utf8.ValidString(business) || strings.ContainsRune(business, 0) || utf8.RuneCountInString(business) > 16000 {
			return Result{}, netapi.Fail("llm", "config")
		}
	}
	prompt := promptFor(input.Intent, p.Config)
	if input.Intent == task.Dictation || p.Config.MaxToolRounds == 0 {
		p.Search = nil
	}
	if netapi.Endpoint(p.Config.BaseURL) != nil || p.Config.Model == "" || settings.ValidKey(p.Key) != nil {
		return Result{}, netapi.Fail("llm", "config")
	}
	if len(input.ASRText) > 32000 || strings.TrimSpace(input.ASRText) == "" || !utf8.ValidString(input.ASRText) || strings.ContainsRune(input.ASRText, 0) || len(input.Clarifications) > 8 {
		return Result{}, netapi.Fail("llm", "request")
	}
	total := len(input.ASRText)
	for _, turn := range input.Clarifications {
		if strings.TrimSpace(turn.Question) == "" || len(turn.Question) > 2000 || !utf8.ValidString(turn.Question) || strings.ContainsRune(turn.Question, 0) ||
			strings.TrimSpace(turn.Answer) == "" || len(turn.Answer) > 32000 || !utf8.ValidString(turn.Answer) || strings.ContainsRune(turn.Answer, 0) {
			return Result{}, netapi.Fail("llm", "request")
		}
		total += len(turn.Question) + len(turn.Answer)
	}
	if total > 128<<10 {
		return Result{}, netapi.Fail("llm", "request")
	}
	if p.Search != nil && (p.MaxSearchCalls < 1 || p.MaxSearchCalls > 8) {
		return Result{}, netapi.Fail("llm", "config")
	}
	meta := input
	meta.ASRText = ""
	contextJSON, _ := json.Marshal(meta)
	asrJSON, _ := json.Marshal(input.ASRText)
	history := []message{{Role: "system", Content: prompt}, {Role: "user", Content: "TRUSTED_CONTEXT (JSON data):\n" + string(contextJSON) + "\nASR_TEXT (JSON string, user content/editing request):\n" + string(asrJSON)}}
	for _, turn := range input.Clarifications {
		question, _ := json.Marshal(Result{Status: "clarify", Text: "", Notice: turn.Question})
		answer, _ := json.Marshal(turn.Answer)
		history = append(history, message{Role: "assistant", Content: string(question)},
			message{Role: "user", Content: "CLARIFICATION_ANSWER (JSON string, supplement to original ASR_TEXT):\n" + string(answer)})
	}
	searches := 0
	ids := map[string]bool{}
	sources := []search.Hit{}
	maxToolRounds := p.Config.MaxToolRounds
	if p.Search == nil {
		maxToolRounds = 0
	}
	for round := 0; round <= maxToolRounds; round++ {
		toolsEnabled := p.Search != nil && round < maxToolRounds && searches < p.MaxSearchCalls
		if p.OnStage != nil {
			p.OnStage("llm")
		}
		reply, finish, err := p.request(ctx, history, toolsEnabled)
		if err != nil {
			return Result{}, err
		}
		if len(reply.ToolCalls) == 0 {
			if finish != "stop" {
				return Result{}, netapi.Fail("llm", "response")
			}
			result, err := parseResult(reply.Content)
			if err != nil {
				return Result{}, err
			}
			if input.Intent == task.Dictation && result.Status == "clarify" {
				return Result{}, netapi.Fail("llm", "dictation_clarify")
			}
			result.Sources = sources
			return result, nil
		}
		if !toolsEnabled || finish != "tool_calls" || len(reply.ToolCalls)+searches > p.MaxSearchCalls {
			return Result{}, netapi.Fail("llm", "response")
		}
		type queryCall struct {
			ID    string
			Query string
		}
		queries := make([]queryCall, 0, len(reply.ToolCalls))
		for _, call := range reply.ToolCalls {
			if call.Type != "function" || call.Function.Name != "search_web" || call.ID == "" || len(call.ID) > 200 || ids[call.ID] {
				return Result{}, netapi.Fail("llm", "response")
			}
			var args struct {
				Query string `json:"query"`
			}
			if netapi.StrictJSON([]byte(call.Function.Arguments), &args) != nil || strings.TrimSpace(args.Query) == "" || !utf8.ValidString(args.Query) || strings.ContainsRune(args.Query, 0) || utf8.RuneCountInString(args.Query) > 500 {
				return Result{}, netapi.Fail("llm", "response")
			}
			ids[call.ID] = true
			queries = append(queries, queryCall{call.ID, args.Query})
		}
		reply.Role = "assistant"
		history = append(history, reply)
		for _, call := range queries {
			if p.OnStage != nil {
				p.OnStage("search")
			}
			searches++
			hits, err := p.Search.Search(ctx, call.Query)
			if err != nil {
				return Result{}, err
			}
			sources = append(sources, hits...)
			evidence, _ := json.Marshal(struct {
				Label       string       `json:"label"`
				RetrievedAt time.Time    `json:"retrieved_at"`
				Results     []search.Hit `json:"results"`
			}{"UNTRUSTED_WEB_EVIDENCE", time.Now(), hits})
			if len(evidence) > 16000 {
				return Result{}, netapi.Fail("exa", "large")
			}
			history = append(history, message{Role: "tool", ToolCallID: call.ID, Content: string(evidence)})
		}
	}
	return Result{}, netapi.Fail("llm", "response")
}
func (p Compatible) request(ctx context.Context, history []message, toolsEnabled bool) (message, string, error) {
	format := map[string]any{"type": "json_object"}
	if p.Config.OutputFormat == "json_schema" {
		format = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "input_result", "strict": true, "schema": schema()}}
	} else if p.Config.OutputFormat != "json_object" {
		return message{}, "", netapi.Fail("llm", "config")
	}
	payload := map[string]any{"model": p.Config.Model, "messages": history, "response_format": format}
	if p.Search != nil {
		payload["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": "search_web", "description": "按需检索可验证的外部事实；查询只包含必要关键词，不发送完整背景或转录。", "parameters": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 500}}, "required": []string{"query"}, "additionalProperties": false}}}}
		payload["tool_choice"] = "none"
		if toolsEnabled {
			payload["tool_choice"] = "auto"
		}
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, netapi.ModelPath(p.Config.BaseURL, "chat/completions"), bytes.NewReader(body))
	if err != nil {
		return message{}, "", netapi.Fail("llm", "config")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.Key)
	ctx, cancel := context.WithTimeout(ctx, netapi.Timeout(p.Config.TimeoutSeconds))
	defer cancel()
	data, err := netapi.Do(ctx, p.Client, req, "llm", 2<<20)
	if err != nil {
		return message{}, "", err
	}
	var response struct {
		Choices []struct {
			Message message `json:"message"`
			Finish  string  `json:"finish_reason"`
		} `json:"choices"`
	}
	if netapi.CheckJSON(data) != nil || json.Unmarshal(data, &response) != nil || len(response.Choices) != 1 {
		return message{}, "", netapi.Fail("llm", "response")
	}
	return response.Choices[0].Message, response.Choices[0].Finish, nil
}
