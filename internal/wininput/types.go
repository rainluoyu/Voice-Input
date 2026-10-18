package wininput

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"voice_input/internal/i18n"
)

type Injector interface {
	// Prepare validates text and waits for held modifiers without submitting input.
	// Commit targets the current active position, not a saved window or control.
	Prepare(context.Context, string) (func() error, error)
}

func Normalize(text string) (string, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 8000 {
		return "", i18n.Error("input.invalid")
	}
	for _, r := range text {
		if (unicode.IsControl(r) && r != '\n') || r == 0x2028 || r == 0x2029 {
			return "", i18n.Error("input.controls")
		}
	}
	return text, nil
}

// UnicodeUnits is the single-line path; newlines use clipboard paste instead of
// synthesizing Return, which may send a message in the target application.
func UnicodeUnits(text string) ([]uint16, error) {
	text, err := Normalize(text)
	if err != nil {
		return nil, err
	}
	if strings.Contains(text, "\n") {
		return nil, i18n.Error("input.multiline")
	}
	return utf16.Encode([]rune(text)), nil
}
