//go:build windows && amd64

package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"voice_input/internal/branding"
	"voice_input/internal/hotkey"
	"voice_input/internal/i18n"
	"voice_input/internal/instance"
	"voice_input/internal/pipeline"
	"voice_input/internal/popup"
	"voice_input/internal/settings"
	"voice_input/internal/startup"
	"voice_input/internal/tray"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"voice_input/frontend"
	core "voice_input/internal/app"
	"voice_input/internal/wininput"
)

func main() {
	if handled, code := startup.HandleHelper(os.Args[1:]); handled {
		os.Exit(code)
	}
	lock, err := instance.Acquire()
	if errors.Is(err, instance.ErrAlreadyRunning) {
		return
	}
	if err != nil {
		startupFatal(i18n.M("popup.feedback.instance_failed"))
		return
	}
	defer lock.Close()
	flag.Parse()
	store, err := settings.OpenDefault()
	if err != nil {
		startupFatal(i18n.FromError(err, "popup.feedback.config_open_failed"))
		return
	}
	vault := settings.NativeVault{}
	controller := core.NewWithFactory(func(ctx context.Context, id string, intent core.Intent, notify func(core.Stage, float64)) (core.Processor, core.TaskOptions, error) {
		return pipeline.New(ctx, id, intent, store.Get(), vault, notify)
	}, wininput.Native{}, 30*time.Second)
	desktop := &Desktop{controller: controller, store: store, vault: vault}
	cfg := store.Get()
	desktop.locale.Store(i18n.Resolve(cfg.UILanguage))
	if err := cfg.ValidateReady(); err != nil {
		desktop.setStartupError(i18n.FromError(err, "popup.feedback.settings_invalid"))
	}
	controller.Presentation(cfg.Mode, cfg.Hotkey, cfg.AIHotkey)
	assets, err := fs.Sub(frontend.Assets, "dist")
	if err != nil {
		log.Fatal(i18n.M("popup.feedback.assets_unavailable").Text(desktop.appliedLocale()))
	}
	a := application.New(application.Options{
		Name: branding.Name, Description: branding.Name, Icon: branding.PNG(),
		Services: []application.Service{application.NewService(desktop)},
		Assets:   application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
			WndProcInterceptor: func(_ uintptr, message uint32, _, lp uintptr) (uintptr, bool) {
				if message == 0x0312 { // Dispatch synchronously without private Wails IDs.
					if intent, ok := desktop.shortcutIntent(hotkey.FromMessage(lp)); ok {
						desktop.toggle(intent)
						return 0, true
					}
				}
				return 0, false
			},
		},
		ErrorHandler: func(_ error) { desktop.setStartupError(i18n.M("popup.feedback.desktop_failed")) },
	})
	desktop.app = a
	statusPopup := popup.New(popup.Actions{
		Close: controller.Close, Settings: desktop.ShowSettings, Copy: desktop.CopyResult, KeepOpen: controller.KeepOpen, Reply: desktop.ReplyToClarification,
	})
	desktop.popup = statusPopup
	statusPopup.SetLocale(desktop.appliedLocale())
	menu := a.NewMenu()
	menu.Add(branding.Name).SetEnabled(false)
	desktop.traySettings = menu.Add(i18n.M("popup.tray.settings").Text(desktop.appliedLocale())).OnClick(func(_ *application.Context) { desktop.ShowSettings() })
	desktop.trayCancel = menu.Add(i18n.M("popup.tray.cancel").Text(desktop.appliedLocale())).OnClick(func(_ *application.Context) { controller.Close() })
	menu.AddSeparator()
	desktop.trayQuit = menu.Add(i18n.M("popup.tray.quit").Text(desktop.appliedLocale())).OnClick(func(_ *application.Context) { controller.Close(); a.Quit() })
	trayFailed := func(err error) {
		log.Printf("tray: %v", err)
		desktop.setStartupError(i18n.M("popup.feedback.tray_failed"))
	}
	desktop.tray = tray.New(tray.Options{
		Icon: branding.PNG(), Tooltip: shortcutTooltip(cfg), Menu: menu,
		OnClick: desktop.ShowSettings,
		OnError: func(err error) { trayFailed(err); desktop.ShowSettings() },
	})
	for _, intent := range []core.Intent{core.Dictation, core.Dialogue} {
		key, _ := hotkey.Parse(cfg.HotkeyFor(intent))
		if err := a.GlobalShortcut.Register(key.Accelerator(), func() { desktop.hotkeyFired(key) }); err != nil {
			desktop.setStartupError(i18n.M("popup.feedback.shortcut_failed", "shortcuts", cfg.HotkeyFor(intent)))
		}
	}
	// Normal startup remains tray-only; incomplete settings and startup failures
	// can be corrected without attempting a recording or finding the tray first.
	started, quit := make(chan struct{}), make(chan struct{})
	var startOnce sync.Once
	a.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(_ *application.ApplicationEvent) {
		application.InvokeSync(func() {
			select {
			case <-quit:
				return
			default:
			}
			startOnce.Do(func() { close(started) })
			if err := desktop.tray.Start(); err != nil {
				trayFailed(err)
			}
			missing := []string{}
			for _, intent := range []core.Intent{core.Dictation, core.Dialogue} {
				key, _ := hotkey.Parse(cfg.HotkeyFor(intent))
				if !a.GlobalShortcut.IsRegistered(key.Accelerator()) {
					missing = append(missing, cfg.HotkeyFor(intent))
				}
			}
			if len(missing) != 0 {
				desktop.setStartupError(i18n.M("popup.feedback.shortcut_failed", "shortcuts", strings.Join(missing, " / ")))
			}
			if desktop.startupError.Load() != nil {
				desktop.ShowSettings()
			}
		})
	})
	// Re-read on the UI thread so queued updates cannot show an obsolete task.
	a.OnShutdown(func() {
		close(quit)
		desktop.tray.Destroy()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := controller.Shutdown(ctx); err != nil {
			log.Print(i18n.FromError(err, "audio.restore_failed").Text(desktop.appliedLocale()))
		}
		statusPopup.Destroy()
	})
	go func() {
		select {
		case <-started:
		case <-quit:
			return
		}
		for {
			select {
			case <-quit:
				return
			case <-controller.Changes():
				application.InvokeSync(func() {
					select {
					case <-quit:
						return
					default:
					}
					state := desktop.Snapshot()
					if err := statusPopup.Update(state, desktop.popupAnchor); err != nil {
						desktop.setStartupError(i18n.M("popup.feedback.popup_failed"))
						controller.Close()
						return
					}
					a.Event.Emit("task:state", state)
				})
			}
		}
	}()
	if err := a.Run(); err != nil {
		log.Fatal(i18n.M("popup.feedback.start_failed", "name", branding.Name).Text(desktop.appliedLocale()))
	}
}
