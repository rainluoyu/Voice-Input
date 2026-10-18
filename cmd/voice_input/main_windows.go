//go:build windows && amd64

package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"log"
	"sync"
	"time"

	"voice_input/internal/branding"
	"voice_input/internal/hotkey"
	"voice_input/internal/instance"
	"voice_input/internal/pipeline"
	"voice_input/internal/popup"
	"voice_input/internal/settings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"voice_input/frontend"
	core "voice_input/internal/app"
	"voice_input/internal/wininput"
)

func main() {
	lock, err := instance.Acquire()
	if errors.Is(err, instance.ErrAlreadyRunning) {
		return
	}
	if err != nil {
		startupFatal("无法获取单实例锁")
		return
	}
	defer lock.Close()
	flag.Parse()
	store, err := settings.OpenDefault()
	if err != nil {
		startupFatal(err.Error())
		return
	}
	vault := settings.NativeVault{}
	controller := core.NewWithFactory(func(ctx context.Context, id string, intent core.Intent, notify func(core.Stage, float64)) (core.Processor, core.TaskOptions, error) {
		return pipeline.New(ctx, id, intent, store.Get(), vault, notify)
	}, wininput.Native{}, 30*time.Second)
	desktop := &Desktop{controller: controller, store: store, vault: vault}
	cfg := store.Get()
	if err := cfg.ValidateReady(); err != nil {
		desktop.startupError.Store(err.Error())
	}
	controller.Presentation(cfg.Mode, cfg.Hotkey, cfg.AIHotkey)
	assets, err := fs.Sub(frontend.Assets, "dist")
	if err != nil {
		log.Fatal("前端资源不可用；请先 npm run build")
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
		ErrorHandler: func(_ error) { desktop.startupError.Store("桌面或快捷键初始化失败，请检查设置") },
	})
	desktop.app = a
	statusPopup := popup.New(popup.Actions{
		Close: controller.Close, Settings: desktop.ShowSettings, Copy: desktop.CopyResult, KeepOpen: controller.KeepOpen, Reply: controller.Reply,
	})
	tray := a.SystemTray.New()
	desktop.tray = tray
	tray.SetIcon(branding.PNG())
	tray.SetTooltip(shortcutTooltip(cfg))
	menu := a.NewMenu()
	menu.Add(branding.Name).SetEnabled(false)
	menu.Add("设置").OnClick(func(_ *application.Context) { desktop.ShowSettings() })
	menu.Add("取消任务 / 隐藏浮窗").OnClick(func(_ *application.Context) { controller.Close() })
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(_ *application.Context) { controller.Close(); a.Quit() })
	tray.SetMenu(menu)
	tray.OnClick(func() { desktop.ShowSettings() })
	for _, intent := range []core.Intent{core.Dictation, core.Dialogue} {
		key, _ := hotkey.Parse(cfg.HotkeyFor(intent))
		if err := a.GlobalShortcut.Register(key.Accelerator(), func() { desktop.hotkeyFired(key) }); err != nil {
			desktop.startupError.Store(intent.Label() + "快捷键无法注册")
		}
	}
	// Normal startup remains tray-only; incomplete settings and startup failures
	// can be corrected without attempting a recording or finding the tray first.
	started := make(chan struct{})
	var startOnce sync.Once
	a.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(_ *application.ApplicationEvent) {
		startOnce.Do(func() { close(started) })
		missing := ""
		for _, intent := range []core.Intent{core.Dictation, core.Dialogue} {
			key, _ := hotkey.Parse(cfg.HotkeyFor(intent))
			if !a.GlobalShortcut.IsRegistered(key.Accelerator()) {
				missing += intent.Label() + "（" + cfg.HotkeyFor(intent) + "） "
			}
		}
		if missing != "" {
			desktop.startupError.Store("快捷键注册失败：" + missing)
		}
		if desktop.StartupError() != "" {
			desktop.ShowSettings()
		}
	})
	// Re-read on the UI thread so queued updates cannot show an obsolete task.
	quit := make(chan struct{})
	a.OnShutdown(func() { close(quit); controller.Close(); statusPopup.Destroy() })
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
					state := controller.Snapshot()
					if err := statusPopup.Update(state, desktop.popupAnchor); err != nil {
						desktop.startupError.Store("浮窗初始化失败，请重启程序")
						controller.Close()
						return
					}
					a.Event.Emit("task:state", state)
				})
			}
		}
	}()
	if err := a.Run(); err != nil {
		log.Fatal(branding.Name + "启动失败，请检查 WebView2 Runtime")
	}
}
