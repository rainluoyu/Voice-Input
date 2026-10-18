package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode"

	"voice_input/internal/i18n"
	"voice_input/internal/settings"
	"voice_input/internal/task"
)

func TestLocaleBusinessDefaultsOnWire(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.EN, i18n.ZhCN} {
		for _, intent := range []task.Intent{task.Dictation, task.Dialogue} {
			for _, override := range []string{"", " \t\r\n　", " \nCUSTOM: preserve this byte for byte. 中文\n ", "opposite_default"} {
				t.Run(string(locale)+"/"+string(intent)+"/"+override, func(t *testing.T) {
					defaults := DefaultPromptsForLocale(locale)
					otherLocale := i18n.EN
					if locale == i18n.EN {
						otherLocale = i18n.ZhCN
					}
					otherDefaults := DefaultPromptsForLocale(otherLocale)
					purpose, fallback, otherDefault := dialoguePurpose, defaults.Dialogue, otherDefaults.Dialogue
					if intent == task.Dictation {
						purpose, fallback, otherDefault = dictationPurpose, defaults.Dictation, otherDefaults.Dictation
					}
					custom := override
					if override == "opposite_default" {
						custom = otherDefault
					}
					business := custom
					if strings.TrimSpace(business) == "" {
						business = fallback
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body struct {
							Messages []message      `json:"messages"`
							Format   map[string]any `json:"response_format"`
							Tools    []any          `json:"tools"`
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 {
							t.Error("invalid request", err)
							w.WriteHeader(400)
							return
						}
						if got, want := body.Messages[0].Content, purpose+businessStart+business+protocolStart+commonRules; got != want {
							t.Errorf("business default/custom or fixed rules changed for %s %s", locale, intent)
						}
						if body.Format["type"] != "json_schema" || len(body.Tools) != 0 {
							t.Error("locale changed protocol or tool gate")
						}
						wire(w, `{"status":"insert","text":"原文语言 unchanged.","notice":""}`, nil)
					}))
					defer server.Close()
					p := provider(server)
					p.Locale = locale
					p.Config.DictationPrompt, p.Config.DialoguePrompt = custom, custom
					r := input()
					r.Intent = intent
					result, err := p.Complete(context.Background(), r)
					if err != nil || result.Text != "原文语言 unchanged." {
						t.Fatal("locale changed model result", err)
					}
				})
			}
		}
	}
}

func TestLocaleDoesNotChangeToolAndClarificationGates(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.EN, i18n.ZhCN} {
		for _, intent := range []task.Intent{task.Dictation, task.Dialogue} {
			for _, rounds := range []int{0, 1} {
				for _, malicious := range []bool{false, true} {
					name := string(locale) + "/" + string(intent) + "/"
					if rounds == 0 {
						name += "disabled"
					} else {
						name += "one_round"
					}
					if malicious {
						name += "/forbidden_tool"
					} else {
						name += "/clarify"
					}
					t.Run(name, func(t *testing.T) {
						var calls atomic.Int32
						searcher := &fakeSearch{}
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							round := calls.Add(1)
							var body struct {
								Tools  []any  `json:"tools"`
								Choice string `json:"tool_choice"`
							}
							if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
								t.Error(err)
							}
							enabled := intent == task.Dialogue && rounds == 1
							if !enabled {
								if len(body.Tools) != 0 || body.Choice != "" {
									t.Error("locale exposed forbidden tool")
								}
							} else {
								wantChoice := "auto"
								if round > 1 {
									wantChoice = "none"
								}
								if len(body.Tools) != 1 || body.Choice != wantChoice {
									t.Error("locale changed tool/search budget gate")
								}
							}
							if enabled && round == 1 {
								wire(w, "", []toolCall{searchCall("allowed")})
								return
							}
							if malicious {
								wire(w, "", []toolCall{searchCall("forbidden")})
								return
							}
							wire(w, `{"status":"clarify","text":"","notice":"模型原始问题？"}`, nil)
						}))
						defer server.Close()
						p := provider(server)
						p.Locale, p.Search = locale, searcher
						p.Config.MaxToolRounds, p.MaxSearchCalls = rounds, 1
						r := input()
						r.Intent = intent
						result, err := p.Complete(context.Background(), r)
						wantError := malicious || intent == task.Dictation
						if (err != nil) != wantError {
							t.Fatal("clarification/tool gate changed", result, err)
						}
						if !wantError && result.Notice != "模型原始问题？" {
							t.Fatal("model notice translated")
						}
						wantCalls, wantSearches := int32(1), int32(0)
						if intent == task.Dialogue && rounds == 1 {
							wantCalls, wantSearches = 2, 1
						}
						if calls.Load() != wantCalls || searcher.calls.Load() != wantSearches {
							t.Fatal("locale changed budget, retries or finalization")
						}
					})
				}
			}
		}
	}
}

func TestEnglishDefaultsDoNotRequireEnglishOutput(t *testing.T) {
	if DefaultPromptsForLocale(i18n.ZhCN) != DefaultPrompts() {
		t.Fatal("Chinese defaults changed")
	}
	for _, text := range []string{DefaultPromptsForLocale(i18n.EN).Dictation, DefaultPromptsForLocale(i18n.EN).Dialogue} {
		for _, r := range text {
			if unicode.Is(unicode.Han, r) {
				t.Fatal("English default contains a Chinese example")
			}
		}
		if strings.Contains(strings.ToLower(text), "output in english") || strings.Contains(strings.ToLower(text), "respond in english") {
			t.Fatal("UI language became an output-language instruction")
		}
	}
	// Empty sentinels alone select defaults; equal text is never inferred as a sentinel.
	cfg := settings.LLM{DictationPrompt: defaultDictationBusiness, DialoguePrompt: defaultDialogueBusiness}
	if promptFor(task.Dictation, cfg, i18n.EN) != DictationPrompt || promptFor(task.Dialogue, cfg, i18n.EN) != SystemPrompt {
		t.Fatal("an explicit Chinese default was incorrectly replaced under English UI")
	}
}
