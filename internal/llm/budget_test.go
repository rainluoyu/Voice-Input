package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"voice_input/internal/netapi"
)

func TestToolRoundAndSearchBudgets(t *testing.T) {
	for _, tt := range []struct {
		name          string
		rounds, calls int
		batches       []int
	}{
		{"zero", 0, 0, nil},
		{"one", 1, 8, []int{1}},
		{"two", 2, 8, []int{1, 1}},
		{"eight", 8, 8, []int{1, 1, 1, 1, 1, 1, 1, 1}},
		{"single_batch_eight_queries", 1, 8, []int{8}},
		{"two_batches_six_queries", 2, 8, []int{3, 3}},
		{"search_exhausted_first_round", 8, 1, []int{1}},
		{"search_exhausted_second_round", 8, 3, []int{2, 1}},
		{"early_stop", 8, 8, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var modelCalls atomic.Int32
			searcher := &fakeSearch{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				round := int(modelCalls.Add(1)) - 1
				var payload struct {
					Messages   []message `json:"messages"`
					Tools      []any     `json:"tools"`
					ToolChoice string    `json:"tool_choice"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if round > len(tt.batches) {
					t.Error("unexpected extra model request")
					http.Error(w, "extra request", 400)
					return
				}
				used, historyLength := 0, 2
				for _, batch := range tt.batches[:round] {
					used += batch
					historyLength += 1 + batch
				}
				if len(payload.Messages) != historyLength || int(searcher.calls.Load()) != used {
					t.Error("batch history or search accounting is incorrect")
				}
				enabled := round < tt.rounds && used < tt.calls
				if tt.rounds == 0 {
					if len(payload.Tools) != 0 || payload.ToolChoice != "" {
						t.Error("zero budget exposed search tool")
					}
				} else if len(payload.Tools) != 1 || (enabled && payload.ToolChoice != "auto") || (!enabled && payload.ToolChoice != "none") {
					t.Errorf("tools not gated on wire: round=%d used=%d choice=%q", round, used, payload.ToolChoice)
				}
				if round == len(tt.batches) {
					wire(w, `{"status":"insert","text":"有证据的结果。","notice":""}`, nil)
					return
				}
				calls := make([]toolCall, tt.batches[round])
				for i := range calls {
					calls[i] = searchCall(fmt.Sprintf("round-%d-query-%d", round, i))
				}
				wire(w, "", calls)
			}))
			defer server.Close()
			p := provider(server)
			p.Config.MaxToolRounds, p.MaxSearchCalls, p.Search = tt.rounds, tt.calls, searcher
			result, err := p.Complete(context.Background(), input())
			wantSearches := 0
			for _, batch := range tt.batches {
				wantSearches += batch
			}
			if err != nil || result.Status != "insert" || len(result.Sources) != wantSearches || modelCalls.Load() != int32(len(tt.batches)+1) || searcher.calls.Load() != int32(wantSearches) {
				t.Fatalf("budget accounting: result=%+v err=%v model=%d search=%d", result, err, modelCalls.Load(), searcher.calls.Load())
			}
		})
	}
}

func TestForbiddenToolResultsDoNotTriggerExtraFinalization(t *testing.T) {
	for _, tt := range []struct {
		name                      string
		rounds, calls, wantModels int
		wantSearches              int
	}{
		{"zero", 0, 8, 1, 0},
		{"last_round_one", 1, 8, 2, 1},
		{"last_round_two", 2, 8, 3, 2},
		{"last_round_eight", 8, 8, 9, 8},
		{"searches_exhausted", 8, 1, 2, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var modelCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				round := modelCalls.Add(1)
				wire(w, "", []toolCall{searchCall(fmt.Sprint(round))})
			}))
			defer server.Close()
			searcher := &fakeSearch{}
			p := provider(server)
			p.Config.MaxToolRounds, p.MaxSearchCalls, p.Search = tt.rounds, tt.calls, searcher
			if _, err := p.Complete(context.Background(), input()); err == nil {
				t.Fatal("model ignored disabled tools but was accepted")
			}
			if int(modelCalls.Load()) != tt.wantModels || int(searcher.calls.Load()) != tt.wantSearches {
				t.Fatalf("violation retried/finalized/searched: model=%d search=%d", modelCalls.Load(), searcher.calls.Load())
			}
		})
	}
}

func TestInvalidOrOversizedToolBatchIsRejectedAsAWhole(t *testing.T) {
	for _, kind := range []string{"over_budget", "remaining_budget", "unknown", "duplicate_in_batch", "duplicate_history", "arguments", "blank", "NUL", "long_query", "bad_type", "bad_id", "bad_finish"} {
		t.Run(kind, func(t *testing.T) {
			var modelCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				round := modelCalls.Add(1)
				if round == 1 && (kind == "remaining_budget" || kind == "duplicate_history") {
					wire(w, "", []toolCall{searchCall("previous")})
					return
				}
				calls := []toolCall{searchCall("valid"), searchCall("invalid")}
				switch kind {
				case "over_budget", "remaining_budget":
					calls = append(calls, searchCall("third"))
				case "unknown":
					calls[1].Function.Name = "exec_shell"
				case "duplicate_in_batch":
					calls[1].ID = calls[0].ID
				case "duplicate_history":
					calls[1].ID = "previous"
				case "arguments":
					calls[1].Function.Arguments = `{"query":"q","extra":true}`
				case "blank":
					calls[1].Function.Arguments = `{"query":"　 "}`
				case "NUL":
					calls[1].Function.Arguments = `{"query":"q\u0000"}`
				case "long_query":
					calls[1].Function.Arguments = `{"query":"` + strings.Repeat("字", 501) + `"}`
				case "bad_type":
					calls[1].Type = "custom"
				case "bad_id":
					calls[1].ID = ""
				case "bad_finish":
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message{ToolCalls: calls}, "finish_reason": "length"}}})
					return
				}
				wire(w, "", calls)
			}))
			defer server.Close()
			searcher := &fakeSearch{}
			p := provider(server)
			p.Config.MaxToolRounds, p.MaxSearchCalls, p.Search = 8, 8, searcher
			if kind == "over_budget" {
				p.MaxSearchCalls = 2
			}
			if kind == "remaining_budget" {
				p.MaxSearchCalls = 3
			}
			if _, err := p.Complete(context.Background(), input()); err == nil {
				t.Fatal("invalid tool batch accepted")
			}
			wantSearches := int32(0)
			if kind == "remaining_budget" || kind == "duplicate_history" {
				wantSearches = 1
			}
			if searcher.calls.Load() != wantSearches || modelCalls.Load() != wantSearches+1 {
				t.Fatalf("invalid batch partly executed or retried: model=%d search=%d", modelCalls.Load(), searcher.calls.Load())
			}
		})
	}
}

func TestBudgetsResetForEachClarificationCompletion(t *testing.T) {
	var modelCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if modelCalls.Add(1)%2 == 1 {
			wire(w, "", []toolCall{searchCall("same-id-new-completion")})
			return
		}
		wire(w, `{"status":"clarify","text":"","notice":"还需要什么条件？"}`, nil)
	}))
	defer server.Close()
	searcher := &fakeSearch{}
	p := provider(server)
	p.Config.MaxToolRounds, p.MaxSearchCalls, p.Search = 1, 1, searcher
	r := input()
	for i := 0; i < 3; i++ {
		result, err := p.Complete(context.Background(), r)
		if err != nil || result.Status != "clarify" || len(result.Sources) != 1 {
			t.Fatal("completion budget was shared with previous turn", result, err)
		}
		r.Clarifications = append(r.Clarifications, Clarification{Question: result.Notice, Answer: "补充条件"})
	}
	if modelCalls.Load() != 6 || searcher.calls.Load() != 3 {
		t.Fatal("per-completion accounting incorrect")
	}
}

func TestToolOrModelFailureIsNotRetried(t *testing.T) {
	for _, kind := range []string{"model", "search"} {
		t.Run(kind, func(t *testing.T) {
			var modelCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				modelCalls.Add(1)
				if kind == "model" {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				wire(w, "", []toolCall{searchCall("first"), searchCall("second")})
			}))
			defer server.Close()
			searcher := &fakeSearch{err: netapi.Fail("exa", "network")}
			p := provider(server)
			p.Config.MaxToolRounds, p.MaxSearchCalls, p.Search = 8, 8, searcher
			if _, err := p.Complete(context.Background(), input()); err == nil {
				t.Fatal("failed external request accepted")
			}
			wantSearches := int32(0)
			if kind == "search" {
				wantSearches = 1
			}
			if modelCalls.Load() != 1 || searcher.calls.Load() != wantSearches {
				t.Fatal("failed external request retried or finalized")
			}
		})
	}
}
