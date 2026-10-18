package wininput

import (
	"context"
	"strings"

	"voice_input/internal/i18n"
)

// INPUT is 40 bytes on Windows x64, including the largest union member.
type keyboardInput struct {
	Type  uint32
	Pad   uint32
	VK    uint16
	Scan  uint16
	Flags uint32
	Time  uint32
	Pad2  uint32
	Extra uintptr
	Tail  [8]byte
}

type inputPlan struct {
	clipboard string
	inputs    []keyboardInput
}

func planInput(text string) (inputPlan, error) {
	text, err := Normalize(text)
	if err != nil {
		return inputPlan{}, err
	}
	if strings.Contains(text, "\n") {
		return inputPlan{clipboard: strings.ReplaceAll(text, "\n", "\r\n"), inputs: []keyboardInput{
			{Type: 1, VK: 0x11}, {Type: 1, VK: 'V'},
			{Type: 1, VK: 'V', Flags: 2}, {Type: 1, VK: 0x11, Flags: 2},
		}}, nil
	}
	units, _ := UnicodeUnits(text)
	inputs := make([]keyboardInput, 0, len(units)*2)
	for _, u := range units {
		inputs = append(inputs, keyboardInput{Type: 1, Scan: u, Flags: 4}, keyboardInput{Type: 1, Scan: u, Flags: 6})
	}
	return inputPlan{inputs: inputs}, nil
}

func (p inputPlan) commit(ctx context.Context, clipboard func(string) error, send func([]keyboardInput) uint32, modifiersHeld func() bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if modifiersHeld() {
		return i18n.Error("input.modifiers")
	}
	if p.clipboard != "" {
		if err := clipboard(p.clipboard); err != nil {
			return i18n.Error("input.clipboard")
		}
		if err := ctx.Err(); err != nil {
			return i18n.Error("input.clipboard_cancelled")
		}
		if modifiersHeld() {
			return i18n.Error("input.clipboard_modifiers")
		}
	}
	sent := send(p.inputs)
	if int(sent) != len(p.inputs) {
		if p.clipboard != "" && sent > 0 && sent < 4 {
			// Release only generated keys whose key-up was not submitted. This is
			// cleanup, not a second paste attempt.
			release := []keyboardInput{}
			if sent == 2 {
				release = append(release, keyboardInput{Type: 1, VK: 'V', Flags: 2})
			}
			release = append(release, keyboardInput{Type: 1, VK: 0x11, Flags: 2})
			send(release)
		}
		return i18n.Error("input.partial")
	}
	return nil
}
