package settings

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"voice_input/internal/hotkey"
	"voice_input/internal/i18n"
	"voice_input/internal/netapi"
	"voice_input/internal/task"
)

type Config struct {
	UILanguage string    `json:"ui_language"`
	Version    int       `json:"version"`
	Mode       string    `json:"mode"`
	Hotkey     string    `json:"hotkey"`
	AIHotkey   string    `json:"ai_hotkey"`
	General    General   `json:"general"`
	Recording  Recording `json:"recording"`
	ASR        ASR       `json:"asr"`
	LLM        LLM       `json:"llm"`
	Exa        Exa       `json:"exa"`
	Profile    Profile   `json:"profile"`
	Popup      Popup     `json:"popup"`
	Privacy    Privacy   `json:"privacy"`
}
type General struct {
	StartWithWindows   bool `json:"start_with_windows"`
	StartAsAdmin       bool `json:"start_as_admin"`
	AutoCopy           bool `json:"auto_copy"`
	MuteWhileRecording bool `json:"mute_while_recording"`
}

type Recording struct {
	DeviceID   string `json:"device_id"`
	MaxSeconds int    `json:"max_seconds"`
}
type ASR struct {
	Provider       string `json:"provider"`
	Endpoint       string `json:"endpoint"`
	Model          string `json:"model"`
	Language       string `json:"language"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}
type LLM struct {
	BaseURL            string `json:"base_url"`
	Model              string `json:"model"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	TaskTimeoutSeconds int    `json:"task_timeout_seconds"`
	OutputFormat       string `json:"output_format"`
	DictationPrompt    string `json:"dictation_prompt"`
	DialoguePrompt     string `json:"dialogue_prompt"`
	MaxToolRounds      int    `json:"max_tool_rounds"`
}
type Exa struct {
	Enabled        bool   `json:"enabled"`
	SearchType     string `json:"search_type"`
	MaxCalls       int    `json:"max_calls"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}
type Profile struct {
	// Legacy fields are read for migration only, then merged into the free text.
	Name             string `json:"name,omitempty"`
	Nickname         string `json:"nickname,omitempty"`
	City             string `json:"city,omitempty"`
	Timezone         string `json:"timezone,omitempty"`
	CustomBackground string `json:"custom_background"`
}

func (p Profile) Text() string {
	parts := []string{}
	for _, field := range []struct{ label, value string }{
		{"姓名", p.Name}, {"常用称呼", p.Nickname}, {"城市", p.City}, {"时区", p.Timezone},
	} {
		if field.value != "" {
			parts = append(parts, field.label+"："+field.value)
		}
	}
	if p.CustomBackground != "" {
		parts = append(parts, p.CustomBackground)
	}
	return strings.Join(parts, "\n")
}

// Normalized migrates in memory only; opening an old config never rewrites it.
func (c Config) Normalized() Config {
	if c.UILanguage == "" {
		c.UILanguage = "auto"
	}
	if c.Mode == "mock" {
		c.Mode = "live"
	}
	c.Profile = Profile{CustomBackground: c.Profile.Text()}
	c.Popup.AlwaysOnTop = true
	return c
}

type Popup struct {
	AutoHideSeconds int  `json:"auto_hide_seconds"`
	AlwaysOnTop     bool `json:"always_on_top"`
}
type Privacy struct {
	SaveHistory bool `json:"save_history"`
	LogText     bool `json:"log_text"`
}

func (c Config) HotkeyFor(intent task.Intent) string {
	if intent == task.Dialogue {
		return c.AIHotkey
	}
	return c.Hotkey
}

func Default() Config {
	return Config{UILanguage: "auto", Version: 1, Mode: "live", Hotkey: "Ctrl+Alt+R", AIHotkey: "Ctrl+Alt+A", Recording: Recording{"default", 120}, ASR: ASR{Provider: "openai_compatible", TimeoutSeconds: 45}, LLM: LLM{TimeoutSeconds: 45, TaskTimeoutSeconds: 90, OutputFormat: "json_schema", MaxToolRounds: 2}, Exa: Exa{SearchType: "instant", MaxCalls: 2, TimeoutSeconds: 10}, Popup: Popup{AutoHideSeconds: 30, AlwaysOnTop: true}}
}
func (c Config) Validate() error {
	switch c.UILanguage {
	case "", "auto", string(i18n.EN), string(i18n.ZhCN):
	default:
		return i18n.Error("settings.ui_language")
	}
	if c.Version != 1 {
		return i18n.Error("settings.version")
	}
	if c.Mode != "asr_mock_llm" && c.Mode != "live" {
		return i18n.Error("settings.mode")
	}
	keys := make([]hotkey.Combination, 0, 2)
	for _, raw := range []string{c.Hotkey, c.AIHotkey} {
		if len(raw) == 0 || len(raw) > 80 {
			return i18n.Error("settings.hotkeys_required")
		}
		key, err := hotkey.Parse(raw)
		if err != nil {
			return err
		}
		if key.Modifiers == 2 && key.Key == 'V' {
			return i18n.Error("settings.paste_hotkey")
		}
		keys = append(keys, key)
	}
	if keys[0] == keys[1] {
		return i18n.Error("settings.hotkeys_same")
	}
	if c.Recording.DeviceID == "" || len(c.Recording.DeviceID) > 2048 || c.Recording.MaxSeconds < 1 || c.Recording.MaxSeconds > 300 {
		return i18n.Error("settings.recording")
	}
	for _, raw := range []string{c.ASR.Endpoint, c.LLM.BaseURL} {
		if raw != "" {
			if err := netapi.Endpoint(raw); err != nil {
				return err
			}
		}
	}
	for _, text := range []string{c.ASR.Model, c.LLM.Model} {
		if len(text) > 200 || strings.ContainsAny(text, "\r\n") {
			return i18n.Error("settings.model")
		}
	}
	switch c.ASR.Provider {
	case "", "openai_compatible":
	case "mimo":
		if c.ASR.Language != "" && c.ASR.Language != "auto" && c.ASR.Language != "zh" && c.ASR.Language != "en" {
			return i18n.Error("settings.mimo_language")
		}
	default:
		return i18n.Error("settings.asr_provider")
	}
	if len(c.ASR.Language) > 20 {
		return i18n.Error("settings.asr_language")
	}
	if c.ASR.TimeoutSeconds < 5 || c.ASR.TimeoutSeconds > 180 || c.LLM.TimeoutSeconds < 5 || c.LLM.TimeoutSeconds > 180 || c.LLM.TaskTimeoutSeconds < 10 || c.LLM.TaskTimeoutSeconds > 300 {
		return i18n.Error("settings.timeouts")
	}
	if c.LLM.OutputFormat != "json_schema" && c.LLM.OutputFormat != "json_object" {
		return i18n.Error("settings.output_format")
	}
	if c.LLM.MaxToolRounds < 0 || c.LLM.MaxToolRounds > 8 {
		return i18n.Error("settings.tool_rounds")
	}
	for _, prompt := range []string{c.LLM.DictationPrompt, c.LLM.DialoguePrompt} {
		if !utf8.ValidString(prompt) || strings.ContainsRune(prompt, 0) || utf8.RuneCountInString(prompt) > 16000 {
			return i18n.Error("settings.prompt")
		}
	}
	if c.Exa.SearchType != "instant" && c.Exa.SearchType != "fast" {
		return i18n.Error("settings.search_type")
	}
	if c.Exa.MaxCalls < 1 || c.Exa.MaxCalls > 8 || c.Exa.TimeoutSeconds < 8 || c.Exa.TimeoutSeconds > 15 {
		return i18n.Error("settings.search_budget")
	}
	if c.General.StartAsAdmin && !c.General.StartWithWindows {
		return i18n.Error("settings.startup_admin")
	}
	if c.Popup.AutoHideSeconds < 1 || c.Popup.AutoHideSeconds > 3600 {
		return i18n.Error("settings.auto_hide")
	}
	if c.Privacy.SaveHistory || c.Privacy.LogText {
		return i18n.Error("settings.privacy")
	}
	background := c.Profile.Text()
	if !utf8.ValidString(background) || strings.ContainsRune(background, 0) || utf8.RuneCountInString(background) > 16000 {
		return i18n.Error("settings.background")
	}
	_, err := configJSON(c.Normalized())
	return err
}

// Include indentation, JSON escaping and the trailing newline used on disk.
const maxConfigBytes = 512 << 10

func configJSON(c Config) ([]byte, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, i18n.Error("settings.encode")
	}
	if len(data)+1 > maxConfigBytes {
		return nil, i18n.Error("settings.large")
	}
	return append(data, '\n'), nil
}

// ValidateReady is checked before a real task, not when opening unfinished settings.
func (c Config) ValidateReady() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.ASR.Endpoint == "" || strings.TrimSpace(c.ASR.Model) == "" {
		return i18n.Error("settings.asr_required")
	}
	if c.Mode == "live" && (c.LLM.BaseURL == "" || strings.TrimSpace(c.LLM.Model) == "") {
		return i18n.Error("settings.llm_required")
	}
	return nil
}

type Store struct {
	mu     sync.RWMutex
	path   string
	config Config
}

func Open(path string) (*Store, error) { return open(path, true) }

func open(path string, missingOK bool) (*Store, error) {
	s := &Store{path: path, config: Default()}
	file, err := os.Open(path)
	if os.IsNotExist(err) && missingOK {
		return s, nil
	}
	if err != nil {
		return nil, i18n.Error("settings.read")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return nil, i18n.Error("settings.read")
	}
	if len(data) > maxConfigBytes {
		return nil, i18n.Error("settings.large")
	}
	if err := netapi.StrictJSON(data, &s.config); err != nil {
		return nil, i18n.Error("settings.format")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(data, &fields)
	if _, exists := fields["ai_hotkey"]; !exists {
		oldKey, _ := hotkey.Parse(s.config.Hotkey)
		defaultAI, _ := hotkey.Parse(s.config.AIHotkey)
		if oldKey == defaultAI {
			s.config.AIHotkey = "Ctrl+Alt+Shift+A"
		}
	}
	s.config = s.config.Normalized()
	if err := s.config.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Store) Get() Config { s.mu.RLock(); defer s.mu.RUnlock(); return s.config }
func (s *Store) Save(c Config) error {
	return s.saveWithPublish(c, func(temp, target string) error {
		if os.Rename(temp, target) != nil {
			return i18n.Error("settings.replace")
		}
		return nil
	})
}

func (s *Store) saveWithPublish(c Config, publish func(temp, target string) error) error {
	c = c.Normalized()
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := configJSON(c)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Dir(s.path)
	if os.MkdirAll(dir, 0700) != nil {
		return i18n.Error("settings.mkdir")
	}
	file, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return i18n.Error("settings.write")
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return i18n.Error("settings.write_failed")
	}
	if err := publish(temp, s.path); err != nil {
		return err
	}
	s.config = c
	return nil
}
