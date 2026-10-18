package hotkey

import (
	"strconv"
	"strings"

	"voice_input/internal/i18n"
	"voice_input/internal/task"
)

// Combination uses the Win32 WM_HOTKEY modifier bits and virtual-key code.
type Combination struct {
	Modifiers uint16
	Key       uint16
}

func FromMessage(lp uintptr) Combination {
	return Combination{uint16(lp) & 0xf, uint16(lp >> 16)}
}

func Resolve(key Combination, dictation, dialogue string) (task.Intent, bool) {
	for _, binding := range []struct {
		raw    string
		intent task.Intent
	}{{dictation, task.Dictation}, {dialogue, task.Dialogue}} {
		parsed, err := Parse(binding.raw)
		if err == nil && parsed == key {
			return binding.intent, true
		}
	}
	return "", false
}

func Parse(raw string) (Combination, error) {
	var result Combination
	parts := strings.Split(strings.ToLower(strings.TrimSpace(raw)), "+")
	for _, part := range parts[:len(parts)-1] {
		switch strings.TrimSpace(part) {
		case "alt", "option", "optionoralt":
			result.Modifiers |= 1
		case "ctrl", "control", "cmd", "command", "cmdorctrl":
			result.Modifiers |= 2
		case "shift":
			result.Modifiers |= 4
		case "win", "super":
			result.Modifiers |= 8
		default:
			return Combination{}, i18n.Error("hotkey.modifier")
		}
	}
	key := strings.TrimSpace(parts[len(parts)-1])
	if len(key) == 1 && ((key[0] >= 'a' && key[0] <= 'z') || (key[0] >= '0' && key[0] <= '9')) {
		result.Key = uint16(strings.ToUpper(key)[0])
	} else if n, err := strconv.Atoi(strings.TrimPrefix(key, "f")); strings.HasPrefix(key, "f") && err == nil && n >= 1 && n <= 24 {
		result.Key = uint16(0x70 + n - 1)
	} else {
		result.Key = namedKeys[key]
	}
	if result.Key == 0 {
		return Combination{}, i18n.Error("hotkey.key")
	}
	return result, nil
}

// Accelerator emits a Wails-compatible spelling, including aliases accepted above.
func (c Combination) Accelerator() string {
	parts := []string{}
	for _, m := range []struct {
		bit  uint16
		name string
	}{{2, "Ctrl"}, {1, "Alt"}, {4, "Shift"}, {8, "Super"}} {
		if c.Modifiers&m.bit != 0 {
			parts = append(parts, m.name)
		}
	}
	var key string
	switch {
	case c.Key >= 'A' && c.Key <= 'Z', c.Key >= '0' && c.Key <= '9':
		key = string(rune(c.Key))
	case c.Key >= 0x70 && c.Key <= 0x87:
		key = "F" + strconv.Itoa(int(c.Key-0x70+1))
	default:
		// Use one stable spelling per virtual key, not map iteration order.
		for _, name := range canonicalNames {
			if namedKeys[name] == c.Key {
				key = name
				break
			}
		}
	}
	parts = append(parts, key)
	return strings.Join(parts, "+")
}

var canonicalNames = []string{"backspace", "tab", "enter", "escape", "space", "page up", "page down", "end", "home", "left", "up", "right", "down", "delete", "numlock", ";", "=", ",", "-", ".", "/", "`", "[", "\\", "]", "'"}
var namedKeys = map[string]uint16{
	"backspace": 0x08, "tab": 0x09, "enter": 0x0d, "return": 0x0d,
	"escape": 0x1b, "space": 0x20, "page up": 0x21, "page down": 0x22,
	"end": 0x23, "home": 0x24, "left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28,
	"delete": 0x2e, "numlock": 0x90,
	";": 0xba, "=": 0xbb, "plus": 0xbb, ",": 0xbc, "-": 0xbd, ".": 0xbe,
	"/": 0xbf, "`": 0xc0, "[": 0xdb, "\\": 0xdc, "]": 0xdd, "'": 0xde,
}
