package wininput

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeMultiline(t *testing.T) {
	for _, text := range []string{"第一行\n第二行 📦", "第一行\r\n\r\n  第二行", "x\n", strings.Repeat("字", 7999) + "\r\n"} {
		got, err := Normalize(text)
		if err != nil || got != strings.ReplaceAll(text, "\r\n", "\n") {
			t.Fatal(text, got, err)
		}
	}
	for _, text := range []string{"\n", "x\ry", "x\ty", "x\x00y", "x\x1by", "x y", "x y", string([]byte{255}), strings.Repeat("字", 8000) + "\n"} {
		if _, err := Normalize(text); err == nil {
			t.Fatal("invalid text accepted")
		}
	}
}
func TestSingleLineDoesNotUseClipboard(t *testing.T) {
	plan, err := planInput("中文 📦")
	if err != nil || plan.clipboard != "" {
		t.Fatal(err)
	}
	err = plan.commit(context.Background(), func(string) error { t.Fatal("single-line clipboard access"); return nil }, func(events []keyboardInput) uint32 {
		for _, e := range events {
			if e.VK != 0 || e.Flags&4 == 0 {
				t.Fatal("non-Unicode single-line input")
			}
		}
		return uint32(len(events))
	}, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
}
func TestMultilinePastesOnceWithoutReturn(t *testing.T) {
	plan, err := planInput("中文\r\n\r\n  emoji 📦")
	if err != nil || plan.clipboard != "中文\r\n\r\n  emoji 📦" {
		t.Fatal(err)
	}
	writes, calls := 0, 0
	err = plan.commit(context.Background(), func(text string) error {
		writes++
		if text != plan.clipboard {
			t.Fatal("lost clipboard text")
		}
		return nil
	}, func(events []keyboardInput) uint32 {
		calls++
		if writes != 1 || len(events) != 4 {
			t.Fatal("paste occurred before clipboard or multiple paste")
		}
		for _, e := range events {
			if e.VK == 0x0d || e.VK == 0x09 || e.Scan != 0 {
				t.Fatal("generated Enter/Tab or text key")
			}
		}
		if events[0].VK != 0x11 || events[1].VK != 'V' || events[2].Flags != 2 || events[3].Flags != 2 {
			t.Fatal("wrong paste chord")
		}
		return uint32(len(events))
	}, func() bool { return false })
	if err != nil || calls != 1 || writes != 1 {
		t.Fatal(err, calls, writes)
	}
}
func TestPasteFailuresAndCancellationDoNotSubmit(t *testing.T) {
	for _, failure := range []string{"cancel_before", "clipboard", "cancel_during_clipboard", "modifiers_before", "modifiers_after"} {
		t.Run(failure, func(t *testing.T) {
			plan, _ := planInput("first\nsecond")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure == "cancel_before" {
				cancel()
			}
			writes, sends, checks := 0, 0, 0
			err := plan.commit(ctx, func(string) error {
				writes++
				if failure == "cancel_during_clipboard" {
					cancel()
				}
				if failure == "clipboard" {
					return errors.New("busy")
				}
				return nil
			}, func([]keyboardInput) uint32 { sends++; return 4 }, func() bool {
				checks++
				return failure == "modifiers_before" || (failure == "modifiers_after" && checks > 1)
			})
			if err == nil || sends != 0 {
				t.Fatal("failed/cancelled paste was submitted", err)
			}
			if (failure == "cancel_before" || failure == "modifiers_before") && writes != 0 {
				t.Fatal("cancelled operation changed clipboard")
			}
		})
	}
}
func TestPartialPasteOnlyReleasesGeneratedKeys(t *testing.T) {
	for sent := uint32(0); sent < 4; sent++ {
		plan, _ := planInput("first\nsecond")
		calls := 0
		err := plan.commit(context.Background(), func(string) error { return nil }, func(events []keyboardInput) uint32 {
			calls++
			if calls == 1 {
				return sent
			}
			for _, event := range events {
				if event.Flags != 2 {
					t.Fatal("partial paste retried key-down")
				}
			}
			return uint32(len(events))
		}, func() bool { return false })
		if err == nil || (sent == 0 && calls != 1) || (sent > 0 && calls != 2) {
			t.Fatal("partial submission not handled", err, calls)
		}
	}
}
