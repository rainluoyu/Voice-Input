package pipeline

import (
	"context"
	"errors"
	"time"

	"voice_input/internal/app"
	"voice_input/internal/asr"
	"voice_input/internal/audio"
	"voice_input/internal/i18n"
	"voice_input/internal/llm"
	"voice_input/internal/netapi"
	"voice_input/internal/search"
	"voice_input/internal/settings"
)

type Session struct {
	Locale    i18n.Locale
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
	locale := app.TaskLocale(ctx, i18n.Resolve(cfg.UILanguage))
	if err := cfg.Validate(); err != nil {
		return nil, app.TaskOptions{Locale: locale, Mode: cfg.Mode, Hotkey: cfg.HotkeyFor(intent)}, err
	}
	options := app.TaskOptions{Locale: locale, Mode: cfg.Mode, Hotkey: cfg.HotkeyFor(intent), RecordLimit: time.Duration(cfg.Recording.MaxSeconds) * time.Second, Timeout: time.Duration(cfg.LLM.TaskTimeoutSeconds) * time.Second}
	if !intent.Valid() {
		return nil, options, i18n.Error("app.intent")
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
	session := &Session{Locale: locale, Recorder: audio.New(), ASR: adapter, Config: cfg, TaskID: id, Intent: intent, Notify: notify}
	if cfg.Mode == "live" {
		key, err := vault.Get("llm")
		if err != nil || settings.ValidKey(key) != nil {
			return nil, options, netapi.Fail("llm", "config")
		}
		completion := llm.Compatible{Locale: locale, Client: client, Config: cfg.LLM, Key: key, MaxSearchCalls: cfg.Exa.MaxCalls, OnStage: func(stage string) {
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
		return app.Result{}, i18n.Error("app.intent")
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
		message := i18n.M("app.asr_direct")
		return app.Result{Status: "insert", Text: conversation.Original, Notice: message.Text(s.Locale), NoticeMessage: &message}, nil
	}
	turns := make([]llm.Clarification, len(conversation.Clarifications))
	for i, turn := range conversation.Clarifications {
		turns[i] = llm.Clarification{Question: turn.Question, Answer: turn.Answer}
	}
	r, err := s.LLM.Complete(ctx, llm.Request{Intent: s.Intent, TaskID: s.TaskID, ASRText: conversation.Original, TargetApp: s.TargetApp, NowLocal: time.Now(), Background: s.Config.Profile.Text(), Clarifications: turns})
	return app.Result{Status: r.Status, Text: r.Text, Notice: r.Notice, Sources: r.Sources}, err
}
