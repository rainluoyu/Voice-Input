package wininput

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

type Injector interface {
	// Prepare validates text and waits for held modifiers without submitting input.
	// Commit targets the current active position, not a saved window or control.
	Prepare(context.Context, string) (func() error, error)
}

func Normalize(text string) (string, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 8000 {
		return "", errors.New("结果为空、编码无效或超过 8000 字；请手动复制")
	}
	for _, r := range text {
		if (unicode.IsControl(r) && r != '\n') || r == 0x2028 || r == 0x2029 {
			return "", errors.New("文本包含 Tab、孤立回车或其他控制字符，禁止自动输入；请手动复制")
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
		return nil, errors.New("多行文本需要使用粘贴路径")
	}
	return utf16.Encode([]rune(text)), nil
}
