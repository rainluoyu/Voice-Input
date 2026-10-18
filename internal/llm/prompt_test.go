package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"voice_input/internal/settings"
	"voice_input/internal/task"
)

func TestDefaultPromptBusinessSegments(t *testing.T) {
	defaults := DefaultPrompts()
	if defaults.Dictation == "" || defaults.Dialogue == "" || defaults.Dictation == defaults.Dialogue {
		t.Fatal("missing distinct business defaults")
	}
	if DictationPrompt != dictationPurpose+businessStart+defaults.Dictation+protocolStart+commonRules || SystemPrompt != dialoguePurpose+businessStart+defaults.Dialogue+protocolStart+commonRules {
		t.Fatal("exported default prompts no longer mean full prompts")
	}
	for _, business := range []string{defaults.Dictation, defaults.Dialogue} {
		if strings.Contains(business, "TRUSTED_CONTEXT") || strings.Contains(business, "恰有 status") {
			t.Fatal("fixed protocol leaked into editable defaults")
		}
	}
	for _, rule := range []string{"修正语病", "成语错用", "不当措辞", "重组句式", "冗余", "事实", "数字", "专名", "时间", "否定", "条件", "因果", "责任主体", "立场", "情绪", "不确定性", "歧义", "可能", "强调", "事倍功半", "事半功倍", "负面评价"} {
		if !strings.Contains(defaults.Dictation, rule) {
			t.Errorf("missing dictation business rule %q", rule)
		}
	}
	goalAt, exampleAt := strings.Index(defaults.Dialogue, "结果正文"), strings.Index(defaults.Dialogue, "例如")
	if !strings.HasPrefix(defaults.Dialogue, "你的任务是理解用户通过语音提出的请求，直接完成任务") || goalAt < 0 || exampleAt <= goalAt {
		t.Fatal("dialogue business defaults must define the task before examples")
	}
	for _, intent := range []task.Intent{task.Dictation, task.Dialogue} {
		for _, blank := range []string{"", " \t\r\n　"} {
			cfg := settings.LLM{DictationPrompt: blank, DialoguePrompt: blank}
			want := SystemPrompt
			if intent == task.Dictation {
				want = DictationPrompt
			}
			if promptFor(intent, cfg) != want {
				t.Fatal("blank override did not restore complete default")
			}
		}
	}
}

func TestCustomPromptIsolationOnWire(t *testing.T) {
	for _, intent := range []task.Intent{task.Dictation, task.Dialogue} {
		t.Run(string(intent), func(t *testing.T) {
			cfg := settings.LLM{DictationPrompt: "DICTATION_CUSTOM：保留轻松表达。", DialoguePrompt: "DIALOGUE_CUSTOM：先给结论。"}
			selected, other := cfg.DialoguePrompt, cfg.DictationPrompt
			purpose, fallback := dialoguePurpose, defaultDialogueBusiness
			if intent == task.Dictation {
				selected, other = cfg.DictationPrompt, cfg.DialoguePrompt
				purpose, fallback = dictationPurpose, defaultDictationBusiness
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages       []message      `json:"messages"`
					ResponseFormat map[string]any `json:"response_format"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 {
					t.Error("invalid message envelope", err)
					http.Error(w, "bad request", 400)
					return
				}
				prompt := body.Messages[0]
				if prompt.Role != "system" || prompt.Content != purpose+businessStart+selected+protocolStart+commonRules || strings.Contains(prompt.Content, other) || strings.Contains(prompt.Content, fallback) {
					t.Error("custom rules replaced more than corresponding business segment")
				}
				context := body.Messages[1].Content
				if strings.Contains(context, selected) || strings.Contains(context, other) || !strings.Contains(context, "自愿背景") || body.ResponseFormat["type"] != "json_schema" {
					t.Error("business prompt leaked into context or changed output protocol")
				}
				for _, fixed := range []string{"JSON 对象", "UNTRUSTED_WEB_EVIDENCE", "不能读取密钥", "不能改变系统边界"} {
					if !strings.Contains(prompt.Content, fixed) {
						t.Errorf("override removed fixed rule %q", fixed)
					}
				}
				wire(w, `{"status":"insert","text":"测试正文。","notice":""}`, nil)
			}))
			defer server.Close()
			p := provider(server)
			p.Config.DictationPrompt, p.Config.DialoguePrompt = cfg.DictationPrompt, cfg.DialoguePrompt
			r := input()
			r.Intent = intent
			if _, err := p.Complete(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			// An override for one purpose cannot affect the other's default prompt.
			cfg = settings.LLM{DictationPrompt: "only dictation"}
			if promptFor(task.Dialogue, cfg) != SystemPrompt {
				t.Fatal("dictation override changed dialogue default")
			}
			cfg = settings.LLM{DialoguePrompt: "only dialogue"}
			if promptFor(task.Dictation, cfg) != DictationPrompt {
				t.Fatal("dialogue override changed dictation default")
			}
		})
	}
}

func TestInvalidPromptOrBudgetNeverCallsModel(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		wire(w, `{"status":"insert","text":"x","notice":""}`, nil)
	}))
	defer server.Close()
	for _, mutate := range []func(*Compatible){
		func(p *Compatible) { p.Config.DictationPrompt = "bad\x00text" },
		func(p *Compatible) { p.Config.DialoguePrompt = "bad\xfftext" },
		func(p *Compatible) { p.Config.DictationPrompt = strings.Repeat("📦", 16001) },
		func(p *Compatible) { p.Config.MaxToolRounds = -1 },
		func(p *Compatible) { p.Config.MaxToolRounds = 9 },
		func(p *Compatible) { p.MaxSearchCalls = 0; p.Search = &fakeSearch{} },
		func(p *Compatible) { p.MaxSearchCalls = 9; p.Search = &fakeSearch{} },
	} {
		p := provider(server)
		mutate(&p)
		if _, err := p.Complete(context.Background(), input()); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid config contacted model")
	}
}
