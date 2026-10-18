package settings

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"voice_input/internal/hotkey"
	"voice_input/internal/netapi"
	"voice_input/internal/task"
)

type Config struct {
	Version   int       `json:"version"`
	Mode      string    `json:"mode"`
	Hotkey    string    `json:"hotkey"`
	AIHotkey  string    `json:"ai_hotkey"`
	Recording Recording `json:"recording"`
	ASR       ASR       `json:"asr"`
	LLM       LLM       `json:"llm"`
	Exa       Exa       `json:"exa"`
	Profile   Profile   `json:"profile"`
	Popup     Popup     `json:"popup"`
	Privacy   Privacy   `json:"privacy"`
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
	return Config{Version: 1, Mode: "live", Hotkey: "Ctrl+Alt+R", AIHotkey: "Ctrl+Alt+A", Recording: Recording{"default", 120}, ASR: ASR{Provider: "openai_compatible", TimeoutSeconds: 45}, LLM: LLM{TimeoutSeconds: 45, TaskTimeoutSeconds: 90, OutputFormat: "json_schema", MaxToolRounds: 2}, Exa: Exa{SearchType: "instant", MaxCalls: 2, TimeoutSeconds: 10}, Popup: Popup{AutoHideSeconds: 30, AlwaysOnTop: true}}
}
func (c Config) Validate() error {
	if c.Version != 1 {
		return errors.New("配置版本不受支持")
	}
	if c.Mode != "asr_mock_llm" && c.Mode != "live" {
		return errors.New("工作模式无效")
	}
	keys := make([]hotkey.Combination, 0, 2)
	for _, raw := range []string{c.Hotkey, c.AIHotkey} {
		if len(raw) == 0 || len(raw) > 80 {
			return errors.New("请分别填写语音润色与 AI 对话快捷键")
		}
		key, err := hotkey.Parse(raw)
		if err != nil {
			return err
		}
		if key.Modifiers == 2 && key.Key == 'V' {
			return errors.New("Ctrl+V 用于多行粘贴，不能设置为录音快捷键")
		}
		keys = append(keys, key)
	}
	if keys[0] == keys[1] {
		return errors.New("语音润色与 AI 对话快捷键不能相同")
	}
	if c.Recording.DeviceID == "" || len(c.Recording.DeviceID) > 2048 || c.Recording.MaxSeconds < 1 || c.Recording.MaxSeconds > 300 {
		return errors.New("录音时长应在 1–300 秒之间，且设备不能为空")
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
			return errors.New("模型名称无效")
		}
	}
	switch c.ASR.Provider {
	case "", "openai_compatible":
	case "mimo":
		if c.ASR.Language != "" && c.ASR.Language != "auto" && c.ASR.Language != "zh" && c.ASR.Language != "en" {
			return errors.New("MiMo 语言只支持留空、auto、zh 或 en")
		}
	default:
		return errors.New("ASR 服务只支持 openai_compatible 或 mimo")
	}
	if len(c.ASR.Language) > 20 {
		return errors.New("语言字段过长")
	}
	if c.ASR.TimeoutSeconds < 5 || c.ASR.TimeoutSeconds > 180 || c.LLM.TimeoutSeconds < 5 || c.LLM.TimeoutSeconds > 180 || c.LLM.TaskTimeoutSeconds < 10 || c.LLM.TaskTimeoutSeconds > 300 {
		return errors.New("服务超时应在 5–180 秒，任务超时应在 10–300 秒")
	}
	if c.LLM.OutputFormat != "json_schema" && c.LLM.OutputFormat != "json_object" {
		return errors.New("输出格式只支持 json_schema 或 json_object")
	}
	if c.LLM.MaxToolRounds < 0 || c.LLM.MaxToolRounds > 8 {
		return errors.New("工具轮数应在 0–8 轮之间")
	}
	for _, prompt := range []string{c.LLM.DictationPrompt, c.LLM.DialoguePrompt} {
		if !utf8.ValidString(prompt) || strings.ContainsRune(prompt, 0) || utf8.RuneCountInString(prompt) > 16000 {
			return errors.New("提示词应为有效文本且不超过 16000 字")
		}
	}
	if c.Exa.SearchType != "instant" && c.Exa.SearchType != "fast" {
		return errors.New("搜索类型只支持 instant 或 fast")
	}
	if c.Exa.MaxCalls < 1 || c.Exa.MaxCalls > 8 || c.Exa.TimeoutSeconds < 8 || c.Exa.TimeoutSeconds > 15 {
		return errors.New("搜索最多调用 1–8 次，超时 8–15 秒")
	}
	if c.Popup.AutoHideSeconds != 30 {
		return errors.New("当前版本终态自动隐藏固定为 30 秒")
	}
	if c.Privacy.SaveHistory || c.Privacy.LogText {
		return errors.New("当前版本不允许保存历史或记录用户文本")
	}
	background := c.Profile.Text()
	if !utf8.ValidString(background) || strings.ContainsRune(background, 0) || utf8.RuneCountInString(background) > 16000 {
		return errors.New("背景信息应为有效文本且不超过 16000 字")
	}
	_, err := configJSON(c.Normalized())
	return err
}

// Include indentation, JSON escaping and the trailing newline used on disk.
const maxConfigBytes = 512 << 10

func configJSON(c Config) ([]byte, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, errors.New("配置编码失败")
	}
	if len(data)+1 > maxConfigBytes {
		return nil, errors.New("配置文件超过 512 KiB")
	}
	return append(data, '\n'), nil
}

// ValidateReady is checked before a real task, not when opening unfinished settings.
func (c Config) ValidateReady() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.ASR.Endpoint == "" || strings.TrimSpace(c.ASR.Model) == "" {
		return errors.New("请配置 ASR 地址与模型")
	}
	if c.Mode == "live" && (c.LLM.BaseURL == "" || strings.TrimSpace(c.LLM.Model) == "") {
		return errors.New("请配置 LLM 地址与模型")
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
		return nil, errors.New("无法读取配置文件")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return nil, errors.New("无法读取配置文件")
	}
	if len(data) > maxConfigBytes {
		return nil, errors.New("配置文件超过 512 KiB")
	}
	if err := netapi.StrictJSON(data, &s.config); err != nil {
		return nil, errors.New("配置文件格式无效；请备份后修复，不会覆盖旧文件")
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
			return errors.New("无法替换配置文件，旧配置未改变")
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
		return errors.New("无法创建配置目录")
	}
	file, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return errors.New("无法写入配置")
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
		return errors.New("配置写入失败，旧配置未改变")
	}
	if err := publish(temp, s.path); err != nil {
		return err
	}
	s.config = c
	return nil
}
