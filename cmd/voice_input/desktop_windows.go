//go:build windows && amd64

package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"

	core "voice_input/internal/app"
	"voice_input/internal/asr"
	"voice_input/internal/audio"
	"voice_input/internal/branding"
	"voice_input/internal/buildmeta"
	"voice_input/internal/hotkey"
	"voice_input/internal/llm"
	"voice_input/internal/netapi"
	"voice_input/internal/search"
	"voice_input/internal/settings"
	"voice_input/internal/wininput"
)

type Desktop struct {
	controller   *core.Controller
	settings     *application.WebviewWindow
	app          *application.App
	store        *settings.Store
	vault        settings.Vault
	startupError atomic.Value
	testMu       sync.Mutex
	tray         *application.SystemTray
	popupAnchor  uintptr // UI-thread-only monitor anchor, never an input target.
}
type SettingsView struct {
	Config            settings.Config    `json:"config"`
	Keys              map[string]bool    `json:"keys"`
	Version           string             `json:"version"`
	PromptDefaults    llm.PromptDefaults `json:"prompt_defaults"`
	CredentialMessage string             `json:"credential_message"`
}
type ActionResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}
type DeviceResult struct {
	Devices []audio.Device `json:"devices"`
	Message string         `json:"message"`
}

func (d *Desktop) Snapshot() core.State        { return d.controller.Snapshot() }
func (d *Desktop) ClosePopup()                 { d.controller.Close() }
func (d *Desktop) KeepPopupOpen(taskID string) { d.controller.KeepOpen(taskID) }
func (d *Desktop) ReplyToClarification(taskID string, turn uint64, stop bool) {
	d.controller.Reply(taskID, turn, stop)
}
func (d *Desktop) ShowSettings() {
	application.InvokeSync(func() {
		// Even a hidden Wails window has activation paths during initialization.
		// Create it only when settings are requested or startup needs attention.
		if d.settings == nil {
			d.settings = d.app.Window.NewWithOptions(application.WebviewWindowOptions{
				Name: "settings", Title: branding.Name + " · 设置", URL: "/?view=settings", Width: 760, Height: 800,
				Hidden: true, MinWidth: 520, MinHeight: 500,
			})
			d.settings.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
				e.Cancel()
				d.settings.Hide()
			})
		}
		d.settings.Run()
		d.settings.Show()
		d.settings.Focus()
	})
}
func (d *Desktop) CloseSettings() {
	application.InvokeSync(func() {
		if d.settings != nil {
			d.settings.Hide()
		}
	})
}
func (d *Desktop) StartupError() string {
	if v := d.startupError.Load(); v != nil {
		return v.(string)
	}
	return ""
}
func (d *Desktop) GetSettings() SettingsView {
	keys := map[string]bool{}
	warning := ""
	for _, kind := range []string{"asr", "llm", "exa"} {
		key, err := d.vault.Get(kind)
		keys[kind] = err == nil && key != ""
		if err != nil && !errors.Is(err, settings.ErrNoKey) {
			warning = "Windows 安全凭据不可用；检查用户会话与 Credential Manager"
		}
	}
	return SettingsView{Config: d.store.Get(), Keys: keys, Version: buildmeta.Version, PromptDefaults: llm.DefaultPrompts(), CredentialMessage: warning}
}
func (d *Desktop) SaveSettings(cfg settings.Config) ActionResult {
	if !d.testMu.TryLock() {
		return ActionResult{Message: "连接测试或任务正在初始化，请稍候"}
	}
	defer d.testMu.Unlock()
	if err := cfg.Validate(); err != nil {
		return ActionResult{Message: err.Error()}
	}
	var result ActionResult
	// Wails shortcut operations are main-thread-affine. Do not hold controller
	// state locks while synchronously invoking native UI work.
	application.InvokeSync(func() {
		if d.controller.Busy() {
			result.Message = "请先停止录音或取消当前任务，再保存设置"
			return
		}
		saved, err := hotkey.Replace(d.app.GlobalShortcut, []string{cfg.Hotkey, cfg.AIHotkey}, d.hotkeyFired, func() error { return d.store.Save(cfg) })
		if saved {
			d.controller.Presentation(cfg.Mode, cfg.Hotkey, cfg.AIHotkey)
			if d.tray != nil {
				d.tray.SetTooltip(shortcutTooltip(cfg))
			}
		}
		if err != nil {
			d.startupError.Store(err.Error())
			result.Message = err.Error()
			return
		}
		d.startupError.Store("")
		result = ActionResult{OK: true, Message: "已保存"}
	})
	return result
}
func (d *Desktop) SaveKey(kind, key string) ActionResult {
	if err := d.vault.Set(kind, key); err != nil {
		return ActionResult{Message: err.Error()}
	}
	return ActionResult{OK: true, Message: "已保存"}
}
func (d *Desktop) DeleteKey(kind string) ActionResult {
	if err := d.vault.Delete(kind); err != nil {
		return ActionResult{Message: err.Error()}
	}
	return ActionResult{OK: true, Message: "已删除"}
}
func (d *Desktop) ListDevices(ctx context.Context) DeviceResult {
	result := make(chan DeviceResult, 1)
	go func() {
		devices, err := audio.Devices()
		message := ""
		if err != nil {
			message = "未找到可用麦克风；检查 Windows 麦克风隐私权限与设备"
		}
		result <- DeviceResult{Devices: devices, Message: message}
	}()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	select {
	case devices := <-result:
		return devices
	case <-ctx.Done():
		return DeviceResult{Devices: []audio.Device{}, Message: "设备枚举超时或已取消；请检查设备驱动后重试"}
	}
}
func (d *Desktop) CopyResult(taskID string) error {
	text, err := d.controller.CopyText(taskID)
	if err != nil {
		return err
	}
	if !d.app.Clipboard.SetText(text) {
		return errCopy
	}
	return nil
}
func (d *Desktop) TestConnection(ctx context.Context, kind string) ActionResult {
	if !d.testMu.TryLock() {
		return ActionResult{Message: "已有连接测试或初始化正在运行，请稍候"}
	}
	defer d.testMu.Unlock()
	defer func() {
		message := "连接测试正在运行；结束后再开始录音"
		if d.StartupError() == message {
			d.startupError.CompareAndSwap(message, "")
		}
	}()
	if d.controller.Busy() {
		return ActionResult{Message: "已有任务正在运行，请先结束或取消"}
	}
	if _, err := settings.KeyName(kind); err != nil {
		return ActionResult{Message: err.Error()}
	}
	cfg := d.store.Get()
	key, err := d.vault.Get(kind)
	if err != nil {
		return ActionResult{Message: "安全凭据未配置或不可用；请检查对应服务的密钥"}
	}
	client, err := netapi.Client()
	if err != nil {
		return ActionResult{Message: "代理配置无效；检查 Windows 环境变量 PROXY_URL"}
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	switch kind {
	case "asr":
		var adapter asr.Adapter
		adapter, err = asr.New(client, cfg.ASR, key)
		if err == nil {
			err = adapter.Probe(ctx)
		}
	case "llm":
		cfg.LLM.DictationPrompt, cfg.LLM.DialoguePrompt, cfg.LLM.MaxToolRounds = "", "", 0
		_, err = (llm.Compatible{Client: client, Config: cfg.LLM, Key: key}).Complete(ctx, llm.Request{Intent: core.Dictation, TaskID: "connection-test", ASRText: "测试连接，请只整理这句话。", NowLocal: time.Now()})
	case "exa":
		_, err = (search.Exa{Client: client, Key: key, Config: cfg.Exa}).Search(ctx, "Exa search API official documentation")
	default:
		err = errors.New("unknown service")
	}
	result := ActionResult{OK: err == nil}
	if err != nil {
		result.Message = netapi.SafeMessage(err, "连接测试失败；检查地址、模型、密钥与代理")
	} else {
		result.Message = "连接成功"
	}
	return result
}
func startupFatal(message string) {
	title, _ := windows.UTF16PtrFromString(branding.Name + " · 启动错误")
	text, _ := windows.UTF16PtrFromString(message)
	_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}

type Presentation struct {
	Mode     string `json:"mode"`
	Hotkey   string `json:"hotkey"`
	AIHotkey string `json:"ai_hotkey"`
}

func (d *Desktop) GetPresentation() Presentation {
	cfg := d.store.Get()
	return Presentation{Mode: cfg.Mode, Hotkey: cfg.Hotkey, AIHotkey: cfg.AIHotkey}
}

func shortcutTooltip(cfg settings.Config) string {
	return branding.Name + " · 润色 " + cfg.Hotkey + " / AI " + cfg.AIHotkey
}

func (d *Desktop) shortcutIntent(key hotkey.Combination) (core.Intent, bool) {
	cfg := d.store.Get()
	intent, ok := hotkey.Resolve(key, cfg.Hotkey, cfg.AIHotkey)
	return intent, ok && d.app.GlobalShortcut.IsRegistered(key.Accelerator())
}

func (d *Desktop) hotkeyFired(key hotkey.Combination) {
	application.InvokeSync(func() {
		if intent, ok := d.shortcutIntent(key); ok {
			d.toggle(intent)
		}
	})
}

// Native-only callback; not exposed as a Wails service method.
func (d *Desktop) toggle(intent core.Intent) {
	if !d.testMu.TryLock() {
		d.startupError.Store("连接测试正在运行；结束后再开始录音")
		return
	}
	defer d.testMu.Unlock()
	application.InvokeSync(func() {
		if !d.controller.Busy() {
			d.popupAnchor = wininput.ForegroundWindow()
		}
		d.controller.Toggle(intent)
	})
}
