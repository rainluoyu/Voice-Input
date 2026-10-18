//go:build windows && amd64

package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	core "voice_input/internal/app"
	"voice_input/internal/audio"
	"voice_input/internal/branding"
	"voice_input/internal/i18n"
	"voice_input/internal/llm"
	"voice_input/internal/settings"
)

type desktopVault struct {
	err error
	get func()
}

func (v desktopVault) Get(string) (string, error) {
	if v.get != nil {
		v.get()
	}
	return "", v.err
}
func (v desktopVault) Set(string, string) error { return v.err }
func (v desktopVault) Delete(string) error      { return v.err }

func testDesktop(t *testing.T, locale i18n.Locale) *Desktop {
	t.Helper()
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	d := &Desktop{store: store, vault: desktopVault{err: settings.ErrNoKey}, controller: core.New(nil, nil, 30*time.Second)}
	d.locale.Store(locale)
	return d
}

func TestSettingsViewProvidesIndependentSystemLocaleAndBothDefaults(t *testing.T) {
	d := testDesktop(t, i18n.EN)
	d.vault = desktopVault{err: errors.New("credential backend secret")}
	view := d.GetSettings()
	if view.Locale != i18n.EN || view.SystemLocale != i18n.SystemLocale() {
		t.Fatal("missing applied or independent system locale", view.Locale, view.SystemLocale)
	}
	for _, locale := range []i18n.Locale{i18n.EN, i18n.ZhCN} {
		if !reflect.DeepEqual(view.PromptDefaultsByLocale[locale], llm.DefaultPromptsForLocale(locale)) {
			t.Fatal("missing prompt preview defaults", locale)
		}
	}
	if !reflect.DeepEqual(view.PromptDefaults, llm.DefaultPromptsForLocale(i18n.EN)) ||
		view.CredentialMessageI18n == nil || view.CredentialMessage != view.CredentialMessageI18n.Text(i18n.EN) ||
		strings.Contains(view.CredentialMessage, "secret") {
		t.Fatal("credential warning or current defaults contract changed")
	}
}

func TestSavedWithWarningAppliesLocaleAndKeepsSavedIndependentOfOK(t *testing.T) {
	for _, saved := range []bool{false, true} {
		d := testDesktop(t, i18n.ZhCN)
		before := d.controller.Snapshot()
		called := false
		result := d.completeSettingsSave(saved, i18n.Error("popup.feedback.settings_save_failed"), func() {
			called = true
			d.locale.Store(i18n.EN)
		})
		if called != saved || result.Saved != saved || result.OK || result.MessageI18n == nil {
			t.Fatal("saved warning did not apply independently of OK", saved, result)
		}
		wantLocale := i18n.ZhCN
		if saved {
			wantLocale = i18n.EN
		}
		if result.Message != result.MessageI18n.Text(wantLocale) || d.GetPresentation().Locale != wantLocale {
			t.Fatal("feedback did not use the applied locale", saved, result)
		}
		if !reflect.DeepEqual(before, d.controller.Snapshot()) {
			t.Fatal("locale save changed task revision, turn or timer")
		}
	}
}

func TestStartupFeedbackRendersAppliedLocaleAndPreservesDescriptor(t *testing.T) {
	d := testDesktop(t, i18n.ZhCN)
	message := i18n.M("popup.feedback.popup_failed")
	d.setStartupError(message)
	zh := d.StartupError()
	d.locale.Store(i18n.EN)
	en := d.StartupError()
	if zh.Message == en.Message || en.Message != message.Text(i18n.EN) || !reflect.DeepEqual(en.MessageI18n, &message) {
		t.Fatal("startup descriptor lost or translation was captured too early")
	}
	d.startupError.Store(nil)
	if got := d.StartupError(); got.Message != "" || got.MessageI18n != nil {
		t.Fatal("empty startup error must be a feedback object", got)
	}
}

func TestConnectionMarkerCleanupUsesPointerIdentity(t *testing.T) {
	for _, newerError := range []bool{false, true} {
		d := testDesktop(t, i18n.ZhCN)
		entered, release := make(chan struct{}), make(chan struct{})
		d.vault = desktopVault{err: settings.ErrNoKey, get: func() { close(entered); <-release }}
		done := make(chan ActionResult, 1)
		go func() { done <- d.TestConnection(context.Background(), "llm") }()
		<-entered
		marker := d.testMarker.Load()
		if marker == nil {
			t.Fatal("test marker was not installed")
		}
		d.startupError.Store(marker)
		if newerError {
			// Equal message text is deliberately not sufficient for cleanup.
			next := *marker
			d.startupError.Store(&next)
		}
		d.locale.Store(i18n.EN)
		close(release)
		result := <-done
		if result.OK || result.MessageI18n == nil || d.testMarker.Load() != nil {
			t.Fatal("fake credential failure did not finish the test safely")
		}
		if (d.startupError.Load() != nil) != newerError {
			t.Fatal("marker cleanup cleared a newer error or retained its own marker")
		}
	}
}

func TestDeviceNamesOnlyTranslateExplicitPlaceholders(t *testing.T) {
	defaultName := i18n.M("popup.device.default")
	fallbackName := i18n.M("popup.device.microphone")
	devices := []audio.Device{
		{ID: "default", Name: "系统默认麦克风", NameI18n: &defaultName},
		{ID: "fallback", Name: "麦克风", NameI18n: &fallbackName},
		{ID: "real", Name: "麦克风"},
		{ID: "real-default-name", Name: "系统默认麦克风"},
		{ID: "english", Name: "My microphone"},
	}
	result := presentDevices(devices, i18n.EN)
	if result[0].Name != "System default microphone" || result[1].Name != "Microphone" {
		t.Fatal("application placeholders were not translated", result)
	}
	for i := 2; i < len(devices); i++ {
		if result[i].Name != devices[i].Name || result[i].NameI18n != nil {
			t.Fatal("a real device was guessed from its name", result[i])
		}
	}
	if devices[0].Name != "系统默认麦克风" || devices[1].Name != "麦克风" {
		t.Fatal("presentation mutated the device source slice")
	}
}

func TestBridgeFeedbackJSONAndUnknownErrorsAreSafe(t *testing.T) {
	d := testDesktop(t, i18n.EN)
	result := d.action(false, true, i18n.FromError(errors.New("provider response: SECRET"), "popup.feedback.test_failed"))
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ok", "saved", "message", "message_i18n"} {
		if _, ok := fields[key]; !ok {
			t.Fatal("missing bridge field", key)
		}
	}
	if strings.Contains(string(data), "SECRET") || string(fields["saved"]) != "true" || string(fields["ok"]) != "false" {
		t.Fatal("unsafe provider error or coupled Saved/OK", string(data))
	}
}

func TestBilingualTrayTooltipKeepsBrandAndShortcuts(t *testing.T) {
	cfg := settings.Default()
	for _, locale := range []i18n.Locale{i18n.EN, i18n.ZhCN} {
		got := shortcutTooltipForLocale(cfg, locale)
		for _, value := range []string{branding.Name, cfg.Hotkey, cfg.AIHotkey} {
			if !strings.Contains(got, value) {
				t.Fatal("tray tooltip changed brand or shortcut", locale, got)
			}
		}
		if locale == i18n.EN && strings.Contains(got, "润色") || locale == i18n.ZhCN && !strings.Contains(got, "润色") {
			t.Fatal("tray tooltip language mismatch", locale, got)
		}
	}
}
