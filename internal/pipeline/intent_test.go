package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"voice_input/internal/app"
	"voice_input/internal/llm"
	"voice_input/internal/netapi"
)

type recordingVault struct {
	keyVault
	read []string
}

func (v *recordingVault) Get(kind string) (string, error) {
	v.read = append(v.read, kind)
	return v.keyVault.Get(kind)
}
func TestDictationDoesNotRequireOrReadSearchKey(t *testing.T) {
	cfg := liveConfig()
	cfg.Exa.Enabled = true
	keys := &recordingVault{keyVault: keyVault{"asr": "test-asr", "llm": "test-llm"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, options, err := New(ctx, "dictation", app.Dictation, cfg, keys, nil)
	if err != nil || options.Hotkey != cfg.Hotkey {
		t.Fatal(err)
	}
	if p.(*Session).LLM.(llm.Compatible).Search != nil || strings.Contains(strings.Join(keys.read, ","), "exa") {
		t.Fatal("dictation enabled search/read search credential")
	}
	if _, _, err := New(ctx, "dialogue", app.Dialogue, cfg, keys, nil); err == nil {
		t.Fatal("dialogue bypassed enabled search credential gate")
	}
}
func TestDialogueZeroToolRoundsDoesNotReadSearchKey(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Error("invalid completion request")
		}
		if _, exists := payload["tools"]; exists {
			t.Error("zero tool budget exposed tools")
		}
		if _, exists := payload["tool_choice"]; exists {
			t.Error("zero tool budget retained tool choice")
		}
		if !strings.Contains(string(payload["messages"]), "自定义对话业务") {
			t.Error("factory lost custom prompt config")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": `{"status":"insert","text":"离线结果","notice":""}`}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	cfg := liveConfig()
	cfg.Exa.Enabled = true
	cfg.LLM.MaxToolRounds = 0
	cfg.LLM.BaseURL = server.URL
	cfg.LLM.DialoguePrompt = "自定义对话业务"
	keys := &recordingVault{keyVault: keyVault{"asr": "test-asr", "llm": "test-llm"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor, _, err := New(ctx, "zero-budget", app.Dialogue, cfg, keys, nil)
	if err != nil {
		t.Fatal("zero budget required Exa key", err)
	}
	session := processor.(*Session)
	adapter := session.LLM.(llm.Compatible)
	if adapter.Search != nil || strings.Join(keys.read, ",") != "asr,llm" || adapter.Config.MaxToolRounds != 0 {
		t.Fatal("zero budget read Exa credentials or constructed search provider")
	}
	for _, conversation := range []app.Conversation{
		{Original: "原始请求"},
		{Original: "原始请求", Clarifications: []app.Clarification{{Question: "补充问题", Answer: "补充条件"}}},
	} {
		result, err := session.CompleteConversation(ctx, conversation)
		if err != nil || result.Text != "离线结果" {
			t.Fatal("zero-budget completion failed", result, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("zero budget used more than one model request per completion")
	}
}

func TestASRDiagnosticRejectsDialogueBeforeReadingKeys(t *testing.T) {
	cfg := liveConfig()
	cfg.Mode = "asr_mock_llm"
	keys := &recordingVault{keyVault: keyVault{}}
	_, _, err := New(context.Background(), "dialogue", app.Dialogue, cfg, keys, nil)
	if err == nil || len(keys.read) != 0 || !strings.Contains(netapi.SafeMessage(err, ""), "AI 对话") {
		t.Fatal("diagnostic silently downgraded dialogue", err)
	}
}
func TestSessionPassesFixedIntentToLLM(t *testing.T) {
	for _, intent := range []app.Intent{app.Dictation, app.Dialogue} {
		provider := &capturingLLM{}
		s := Session{Intent: intent, Config: liveConfig(), TaskID: "intent", LLM: provider}
		_, err := s.Complete(context.Background(), "帮我写一封请假邮件")
		if err != nil || provider.request.Intent != intent {
			t.Fatal("intent lost at pipeline", err)
		}
	}
}
