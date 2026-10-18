package netapi

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode"

	"voice_input/internal/i18n"
)

func TestDescriptorsAreLocalizedWithoutProviderErrorExposure(t *testing.T) {
	for _, service := range []string{"asr", "llm", "exa", "audio", "unknown-secret-service"} {
		for _, code := range []string{"mode", "dictation_clarify", "auth", "rate", "server", "network", "timeout", "cancelled", "response", "large", "upload_large", "request", "config", "quiet", "device"} {
			err := fmt.Errorf("unsafe wrapper secret: %w", &Error{Service: service, Code: code, Status: 401})
			m := i18n.FromError(err, "app.llm_failed")
			for _, locale := range []i18n.Locale{i18n.EN, i18n.ZhCN} {
				text := m.Text(locale)
				if text == "" || strings.Contains(text, "secret") || strings.Contains(text, "401") {
					t.Fatal("provider metadata exposed", m, text)
				}
				if locale == i18n.EN {
					for _, r := range text {
						if unicode.Is(unicode.Han, r) {
							t.Fatal("Chinese service/error label in English", text)
						}
					}
				}
			}
		}
	}
	for _, err := range []error{errors.New("语音识别：请求超时"), errors.New("secret-vendor-body"), &Error{Service: "asr", Code: "secret-code"}} {
		m := i18n.FromError(err, "app.llm_failed")
		if m.Code != "app.llm_failed" {
			t.Fatal("unknown error was guessed or exposed", m)
		}
	}
	for _, err := range []error{Endpoint("http://outside.example"), Endpoint("https://user:secret@example.test"), i18n.Error("json.invalid")} {
		m := i18n.FromError(err, "app.llm_failed")
		if m.Code == "app.llm_failed" || m.Text(i18n.EN) == "" {
			t.Fatal("known validation error lost descriptor")
		}
	}
}
