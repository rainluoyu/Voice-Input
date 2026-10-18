package settings

import (
	"strings"

	"voice_input/internal/i18n"
)

type Vault interface {
	Get(string) (string, error)
	Set(string, string) error
	Delete(string) error
}

var ErrNoKey = i18n.Error("settings.no_key")

func KeyName(kind string) (string, error) {
	switch kind {
	case "asr", "llm", "exa":
		return "voice_input/" + kind, nil
	}
	return "", i18n.Error("settings.key_kind")
}
func ValidKey(key string) error {
	if len(key) < 1 || len(key) > 2560 || strings.ContainsAny(key, "\r\n\x00") {
		return i18n.Error("settings.key_invalid")
	}
	return nil
}
