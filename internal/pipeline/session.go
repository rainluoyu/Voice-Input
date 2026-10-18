package pipeline

import (
	"context"
	"errors"
	"time"

	"voice_input/internal/app"
	"voice_input/internal/asr"
	"voice_input/internal/audio"
	"voice_input/internal/llm"
	"voice_input/internal/netapi"
	"voice_input/internal/search"
	"voice_input/internal/settings"
)

type Session struct {
	Recorder  audio.Recorder
	ASR       asr.Provider
	LLM       llm.Provider
	Config    settings.Config
	TaskID    string
	Intent    app.Intent
	TargetApp string
	Notify    func(app.Stage, float64)
}

func New(ctx context.Context, id string, intent app.Intent, cfg settings.Config, vault settings.Vault, notify func(app.Stage, float64)) (app.Processor, app.TaskOptions, error) {
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return nil, app.TaskOptions{Mode: cfg.Mode, Hotkey: cfg.HotkeyFor(intent)}, err
	}
	options := app.TaskOptions{Mode: cfg.Mode, Hotkey: cfg.HotkeyFor(intent), RecordLimit: time.Duration(cfg.Recording.MaxSeconds) * time.Second, Timeout: time.Duration(cfg.LLM.TaskTimeoutSeconds) * time.Second}
	if !intent.Valid() {
		return nil, options, errors.New("任务用途无效")
	}
	if cfg.Mode == "asr_mock_llm" && intent == app.Dialogue {
		return nil, options, netapi.Fail("llm", "mode")
	}
	if err := cfg.ValidateReady(); err != nil {
		return nil, options, err
	}
	client, err := netapi.Client()
	if err != nil {
		return nil, options, netapi.Fail("asr", "config")
	}
	context.AfterFunc(ctx, client.CloseIdleConnections)
	key, err := vault.Get("asr")
	if err != nil || settings.ValidKey(key) != nil {
		return nil, options, netapi.Fail("asr", "config")
	}
	adapter, err := asr.New(client, cfg.ASR, key)
	if err != nil {
		return nil, options, err
	}
	session := &Session{Recorder: audio.New(), ASR: adapter, Config: cfg, TaskID: id, Intent: intent, Notify: notify}
	if cfg.Mode == "live" {
		key, err := vault.Get("llm")
		if err != nil || settings.ValidKey(key) != nil {
			return nil, options, netapi.Fail("llm", "config")
		}
		completion := llm.Compatible{Client: client, Config: cfg.LLM, Key: key, MaxSearchCalls: cfg.Exa.MaxCalls, OnStage: func(stage string) {
			if notify != nil {
				notify(app.Stage(stage), 0)
			}
		}}
		if cfg.Exa.Enabled && intent == app.Dialogue && cfg.LLM.MaxToolRounds > 0 {
			exaKey, err := vault.Get("exa")
			if err != nil || settings.ValidKey(exaKey) != nil {
				return nil, options, netapi.Fail("exa", "config")
			}
			completion.Search = search.Exa{Client: client, Key: exaKey, Config: cfg.Exa}
		}
		session.LLM = completion
	}
	return session, options, nil
}
func (s *Session) Begin(ctx context.Context) error {
	err := s.Recorder.Start(ctx, s.Config.Recording.DeviceID, s.Config.Recording.MaxSeconds, func(level float64) {
		if s.Notify != nil {
			s.Notify(app.Recording, level)
		}
	})
	if err != nil {
		return netapi.Fail("audio", "device")
	}
	return nil
}
func (s *Session) Transcribe(ctx context.Context) (string, error) {
	data, err := s.Recorder.Stop(ctx)
	if err != nil {
		if errors.Is(err, audio.ErrQuiet) {
			return "", netapi.Fail("audio", "quiet")
		}
		return "", netapi.Fail("audio", "device")
	}
	return s.ASR.Transcribe(ctx, data)
}
func (s *Session) Complete(ctx context.Context, raw string) (app.Result, error) {
	return s.CompleteConversation(ctx, app.Conversation{Original: raw})
}

func (s *Session) CompleteConversation(ctx context.Context, conversation app.Conversation) (app.Result, error) {
	if !s.Intent.Valid() {
		return app.Result{}, errors.New("任务用途无效")
	}
	if s.Intent == app.Dictation && len(conversation.Clarifications) != 0 {
		return app.Result{}, netapi.Fail("llm", "request")
	}
	if s.Config.Mode == "asr_mock_llm" && s.Intent == app.Dialogue {
		return app.Result{}, netapi.Fail("llm", "mode")
	}
	if err := conversation.Validate(); err != nil {
		return app.Result{}, err
	}
	if s.Config.Mode == "asr_mock_llm" {
		// M2 mode deliberately passes through real ASR rather than replacing the
		// user's speech with a fixed sample. No semantic API is called.
		return app.Result{Status: "insert", Text: conversation.Original, Notice: "ASR 直通模式：未调用 LLM"}, nil
	}
	turns := make([]llm.Clarification, len(conversation.Clarifications))
	for i, turn := range conversation.Clarifications {
		turns[i] = llm.Clarification{Question: turn.Question, Answer: turn.Answer}
	}
	r, err := s.LLM.Complete(ctx, llm.Request{Intent: s.Intent, TaskID: s.TaskID, ASRText: conversation.Original, TargetApp: s.TargetApp, NowLocal: time.Now(), Background: s.Config.Profile.Text(), Clarifications: turns})
	return app.Result{Status: r.Status, Text: r.Text, Notice: r.Notice, Sources: r.Sources}, err
}
