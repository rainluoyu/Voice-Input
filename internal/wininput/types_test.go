package wininput

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestUnicodeRoundTrip(t *testing.T) {
	for _, text := range []string{"中文输入", "English 日本語", "emoji 📦👩‍💻😀", strings.Repeat("长", 8000)} {
		units, err := UnicodeUnits(text)
		if err != nil || string(utf16.Decode(units)) != text {
			t.Fatalf("round trip failed: %v", err)
		}
	}
	units, _ := UnicodeUnits("📦")
	if len(units) != 2 || units[0] != 0xd83d || units[1] != 0xdce6 {
		t.Fatalf("surrogate pair: %x", units)
	}
}
func TestUnicodeRejectsUnsafeInput(t *testing.T) {
	for _, text := range []string{"", "line\nbreak", "\r", "tab\t", "null\x00", "\x1b", "\x7f", "\u0085", "\u2028", "\u2029", string([]byte{0xff}), strings.Repeat("长", 8001)} {
		if _, err := UnicodeUnits(text); err == nil {
			t.Errorf("accepted unsafe text %q", text)
		}
	}
}
