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
	"voice_input/internal/i18n"
	"voice_input/internal/llm"
	"voice_input/internal/netapi"
	"voice_input/internal/popup"
	"voice_input/internal/search"
	"voice_input/internal/settings"
	"voice_input/internal/startup"
	"voice_input/internal/tray"
	"voice_input/internal/wininput"
)

type Desktop struct {
	controller   *core.Controller
	settings     *application.WebviewWindow
	app          *application.App
	store        *settings.Store
	vault        settings.Vault
	startupError atomic.Pointer[i18n.Message]
	testMarker   atomic.Pointer[i18n.Message]
	locale       atomic.Value // i18n.Locale, applied only on the UI thread.
	testMu       sync.Mutex
	tray         *tray.Window
	traySettings *application.MenuItem
	trayCancel   *application.MenuItem
	trayQuit     *application.MenuItem
	popup        *popup.Window
	popupAnchor  uintptr // UI-thread-only monitor anchor, never an input target.
}
type SettingsView struct {
	Config                 settings.Config                    `json:"config"`
	Keys                   map[string]bool                    `json:"keys"`
	Version                string                             `json:"version"`
	Locale                 i18n.Locale                        `json:"locale"`
	SystemLocale           i18n.Locale                        `json:"system_locale"`
	PromptDefaults         llm.PromptDefaults                 `json:"prompt_defaults"`
	PromptDefaultsByLocale map[i18n.Locale]llm.PromptDefaults `json:"prompt_defaults_by_locale"`
	CredentialMessage      string                             `json:"credential_message"`
	CredentialMessageI18n  *i18n.Message                      `json:"credential_message_i18n,omitempty"`
}
type Feedback struct {
	Message     string        `json:"message"`
	MessageI18n *i18n.Message `json:"message_i18n,omitempty"`
}
type ActionResult struct {
	OK          bool          `json:"ok"`
	Saved       bool          `json:"saved"`
	Message     string        `json:"message"`
	MessageI18n *i18n.Message `json:"message_i18n,omitempty"`
}
type DeviceResult struct {
	Devices     []audio.Device `json:"devices"`
	Message     string         `json:"message"`
	MessageI18n *i18n.Message  `json:"message_i18n,omitempty"`
}

func (d *Desktop) appliedLocale() i18n.Locale {
	if locale := d.locale.Load(); locale != nil {
		return locale.(i18n.Locale)
	}
	return i18n.Resolve(d.store.Get().UILanguage)
}
func (d *Desktop) feedback(message *i18n.Message) Feedback {
	if message == nil {
		return Feedback{}
	}
	return Feedback{Message: message.Text(d.appliedLocale()), MessageI18n: message}
}
func (d *Desktop) action(ok, saved bool, message i18n.Message) ActionResult {
	feedback := d.feedback(&message)
	return ActionResult{OK: ok, Saved: saved, Message: feedback.Message, MessageI18n: feedback.MessageI18n}
}
func (d *Desktop) setStartupError(message i18n.Message) { d.startupError.Store(&message) }
func (d *Desktop) Snapshot() core.State {
	return core.Present(d.controller.Snapshot(), d.appliedLocale())
}
func (d *Desktop) ClosePopup()                 { d.controller.Close() }
func (d *Desktop) KeepPopupOpen(taskID string) { d.controller.KeepOpen(taskID) }
func (d *Desktop) ReplyToClarification(taskID string, turn uint64, stop bool) {
	if !d.testMu.TryLock() {
		return
	}
	defer d.testMu.Unlock()
	d.controller.Reply(taskID, turn, stop)
}
func (d *Desktop) ShowSettings() {
	application.InvokeSync(func() {
		// Even a hidden Wails window has activation paths during initialization.
		// Create it only when settings are requested or startup needs attention.
		if d.settings == nil {
			d.settings = d.app.Window.NewWithOptions(application.WebviewWindowOptions{
				Name: "settings", Title: branding.Name, URL: "/?view=settings", Width: 760, Height: 800,
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
func (d *Desktop) StartupError() Feedback { return d.feedback(d.startupError.Load()) }
func (d *Desktop) GetSettings() SettingsView {
	keys := map[string]bool{}
	var warning *i18n.Message
	for _, kind := range []string{"asr", "llm", "exa"} {
		key, err := d.vault.Get(kind)
		keys[kind] = err == nil && key != ""
		if err != nil && !errors.Is(err, settings.ErrNoKey) {
			message := i18n.M("popup.feedback.credentials_unavailable")
			warning = &message
		}
	}
	locale := d.appliedLocale()
	feedback := d.feedback(warning)
	return SettingsView{
		Config: d.store.Get(), Keys: keys, Version: buildmeta.Version,
		Locale: locale, SystemLocale: i18n.SystemLocale(),
		PromptDefaults: llm.DefaultPromptsForLocale(locale),
		PromptDefaultsByLocale: map[i18n.Locale]llm.PromptDefaults{
			i18n.EN: llm.DefaultPromptsForLocale(i18n.EN), i18n.ZhCN: llm.DefaultPromptsForLocale(i18n.ZhCN),
		},
		CredentialMessage: feedback.Message, CredentialMessageI18n: feedback.MessageI18n,
	}
}
func (d *Desktop) SaveSettings(cfg settings.Config) ActionResult {
	if !d.testMu.TryLock() {
		return d.action(false, false, i18n.M("popup.feedback.initializing"))
	}
	defer d.testMu.Unlock()
	if err := cfg.Validate(); err != nil {
		return d.action(false, false, i18n.FromError(err, "popup.feedback.settings_invalid"))
	}
	var busy bool
	application.InvokeSync(func() { busy = d.controller.Busy() })
	if busy {
		return d.action(false, false, i18n.M("popup.feedback.stop_before_save"))
	}
	previous := d.store.Get()
	var saved bool
	var saveErr error
	// UAC and startup registration wait on this worker, never on the UI thread.
	// testMu keeps hotkeys and reply recording out of the entire transaction.
	err := startup.Save(startup.Options{Enabled: previous.General.StartWithWindows, Admin: previous.General.StartAsAdmin},
		startup.Options{Enabled: cfg.General.StartWithWindows, Admin: cfg.General.StartAsAdmin}, func() error {
			application.InvokeSync(func() {
				saved, saveErr = hotkey.Replace(d.app.GlobalShortcut, []string{cfg.Hotkey, cfg.AIHotkey}, d.hotkeyFired, func() error { return d.store.Save(cfg) })
			})
			if saved {
				return nil // A post-commit shortcut warning must not roll startup back.
			}
			return saveErr
		})
	if err != nil {
		saveErr = err
	}
	var result ActionResult
	application.InvokeSync(func() {
		result = d.completeSettingsSave(saved, saveErr, func() {
			d.applyPresentation(previous, d.store.Get())
		})
	})
	return result
}

func (d *Desktop) completeSettingsSave(saved bool, err error, apply func()) ActionResult {
	if saved {
		apply()
	}
	if err != nil {
		message := i18n.FromError(err, "popup.feedback.settings_save_failed")
		d.setStartupError(message)
		return d.action(false, saved, message)
	}
	d.startupError.Store(nil)
	return d.action(true, saved, i18n.M("popup.feedback.saved"))
}

// applyPresentation runs only on the UI thread after persistence succeeds,
// including a saved-with-shortcut-warning result. A language-only change does
// not publish a task revision, reset its timers, or replay its result follow.
func (d *Desktop) applyPresentation(previous, cfg settings.Config) {
	d.locale.Store(i18n.Resolve(cfg.UILanguage))
	if previous.Mode != cfg.Mode || previous.Hotkey != cfg.Hotkey || previous.AIHotkey != cfg.AIHotkey {
		d.controller.Presentation(cfg.Mode, cfg.Hotkey, cfg.AIHotkey)
	}
	locale := d.appliedLocale()
	if d.tray != nil {
		d.tray.SetTooltip(shortcutTooltipForLocale(cfg, locale))
		d.traySettings.SetLabel(i18n.M("popup.tray.settings").Text(locale))
		d.trayCancel.SetLabel(i18n.M("popup.tray.cancel").Text(locale))
		d.trayQuit.SetLabel(i18n.M("popup.tray.quit").Text(locale))
	}
	if d.popup != nil {
		d.popup.SetLocale(locale)
	}
	d.app.Event.Emit("presentation:changed", d.GetPresentation())
}
func (d *Desktop) SaveKey(kind, key string) ActionResult {
	if err := d.vault.Set(kind, key); err != nil {
		return d.action(false, false, i18n.FromError(err, "popup.feedback.key_save_failed"))
	}
	return d.action(true, true, i18n.M("popup.feedback.saved"))
}
func (d *Desktop) DeleteKey(kind string) ActionResult {
	if err := d.vault.Delete(kind); err != nil {
		return d.action(false, false, i18n.FromError(err, "popup.feedback.key_delete_failed"))
	}
	return d.action(true, true, i18n.M("popup.feedback.deleted"))
}
func presentDevices(devices []audio.Device, locale i18n.Locale) []audio.Device {
	result := append([]audio.Device{}, devices...)
	for i := range result {
		if result[i].NameI18n != nil {
			result[i].Name = result[i].NameI18n.Text(locale)
		}
	}
	return result
}
func (d *Desktop) deviceResult(devices []audio.Device, message *i18n.Message) DeviceResult {
	feedback := d.feedback(message)
	return DeviceResult{Devices: presentDevices(devices, d.appliedLocale()), Message: feedback.Message, MessageI18n: feedback.MessageI18n}
}
func (d *Desktop) ListDevices(ctx context.Context) DeviceResult {
	type enumeration struct {
		devices []audio.Device
		err     error
	}
	result := make(chan enumeration, 1)
	go func() {
		devices, err := audio.Devices()
		result <- enumeration{devices, err}
	}()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	select {
	case devices := <-result:
		var message *i18n.Message
		if devices.err != nil {
			warning := i18n.FromError(devices.err, "popup.feedback.devices_unavailable")
			message = &warning
		}
		return d.deviceResult(devices.devices, message)
	case <-ctx.Done():
		message := i18n.M("popup.feedback.devices_timeout")
		return d.deviceResult(nil, &message)
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
		return d.action(false, false, i18n.M("popup.feedback.test_busy"))
	}
	defer d.testMu.Unlock()
	marker := i18n.M("popup.feedback.test_running")
	d.testMarker.Store(&marker)
	defer func() {
		d.testMarker.CompareAndSwap(&marker, nil)
		d.startupError.CompareAndSwap(&marker, nil)
	}()
	if d.controller.Busy() {
		return d.action(false, false, i18n.M("popup.feedback.task_busy"))
	}
	if _, err := settings.KeyName(kind); err != nil {
		return d.action(false, false, i18n.FromError(err, "popup.feedback.service_invalid"))
	}
	cfg := d.store.Get()
	key, err := d.vault.Get(kind)
	if err != nil {
		return d.action(false, false, i18n.M("popup.feedback.key_unavailable"))
	}
	client, err := netapi.Client(cfg.General.Proxy)
	if err != nil {
		return d.action(false, false, i18n.FromError(err, "popup.feedback.proxy_invalid"))
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
		_, err = (llm.Compatible{Client: client, Config: cfg.LLM, Key: key, Locale: i18n.Resolve(cfg.UILanguage)}).Complete(ctx, llm.Request{Intent: core.Dictation, TaskID: "connection-test", ASRText: "测试连接，请只整理这句话。", NowLocal: time.Now()})
	case "exa":
		_, err = (search.Exa{Client: client, Key: key, Config: cfg.Exa}).Search(ctx, "Exa search API official documentation")
	}
	if err != nil {
		return d.action(false, false, i18n.FromError(err, "popup.feedback.test_failed"))
	}
	return d.action(true, false, i18n.M("popup.feedback.connected"))
}
func startupFatal(message i18n.Message) {
	title, _ := windows.UTF16PtrFromString(branding.Name)
	text, _ := windows.UTF16PtrFromString(message.Text(i18n.SystemLocale()))
	_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}

type Presentation struct {
	Mode     string      `json:"mode"`
	Hotkey   string      `json:"hotkey"`
	AIHotkey string      `json:"ai_hotkey"`
	Locale   i18n.Locale `json:"locale"`
}

func (d *Desktop) GetPresentation() Presentation {
	cfg := d.store.Get()
	return Presentation{Mode: cfg.Mode, Hotkey: cfg.Hotkey, AIHotkey: cfg.AIHotkey, Locale: d.appliedLocale()}
}

func shortcutTooltip(cfg settings.Config) string {
	return shortcutTooltipForLocale(cfg, i18n.Resolve(cfg.UILanguage))
}
func shortcutTooltipForLocale(cfg settings.Config, locale i18n.Locale) string {
	return i18n.M("popup.tray.tooltip", "name", branding.Name, "hotkey", cfg.Hotkey, "ai_hotkey", cfg.AIHotkey).Text(locale)
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
		if marker := d.testMarker.Load(); marker != nil {
			d.startupError.Store(marker)
			// A test may have finished between the failed TryLock and Store.
			if d.testMarker.Load() != marker {
				d.startupError.CompareAndSwap(marker, nil)
			}
		} else {
			d.setStartupError(i18n.M("popup.feedback.initializing"))
		}
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
