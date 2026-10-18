package popup

import (
	"strconv"
	"strings"
	"time"

	"voice_input/internal/app"
	"voice_input/internal/i18n"
)

// Chinese wrappers keep the legacy model contract; UI callers pass a locale.
func stageLabel(s app.State) string { return stageLabelForLocale(s, i18n.ZhCN) }

func stageLabelForLocale(s app.State, locale i18n.Locale) string {
	code := ""
	if s.Stage == app.LLM {
		code = "llm_dictation"
		if s.Intent == app.Dialogue {
			code = "llm_dialogue"
		}
	} else if s.Replying && s.Stage == app.Recording {
		code = "reply_recording"
	} else if s.Replying && s.Stage == app.ASR {
		code = "reply_asr"
	} else {
		switch s.Stage {
		case app.Idle, app.Recording, app.ASR, app.Search, app.Injecting,
			app.Done, app.Skip, app.Clarify, app.Error, app.Cancelled:
			code = string(s.Stage)
		default:
			return ""
		}
	}
	return i18n.M("popup.stage." + code).Text(locale)
}

func purposeLabel(s app.State) string { return purposeLabelForLocale(s, i18n.ZhCN) }

func purposeLabelForLocale(s app.State, locale i18n.Locale) string {
	code := "dictation"
	if s.Intent == app.Dialogue {
		code = "dialogue"
	}
	label := i18n.M("popup.intent." + code).Text(locale)
	if s.Mode == "asr_mock_llm" {
		label += i18n.M("popup.mode.asr_passthrough").Text(locale)
	}
	return label
}

func footer(s app.State, now time.Time) string { return footerForLocale(s, now, i18n.ZhCN) }

func footerForLocale(s app.State, now time.Time, locale i18n.Locale) string {
	if s.Stage == app.Clarify {
		return i18n.M("popup.footer.reply_start", "hotkey", s.Hotkey).Text(locale)
	}
	if s.Stage == app.Recording {
		code := "popup.footer.recording_stop"
		if s.Replying {
			code = "popup.footer.reply_stop"
		}
		return i18n.M(code, "hotkey", s.Hotkey).Text(locale)
	}
	if s.Pinned {
		return i18n.M("popup.footer.pinned").Text(locale)
	}
	if s.HideAt != 0 {
		seconds := max(0, (s.HideAt-now.UnixMilli()+999)/1000)
		return i18n.M("popup.footer.hide_after", "seconds", strconv.FormatInt(seconds, 10)).Text(locale)
	}
	return ""
}

func replyAction(s app.State) string {
	if s.Intent != app.Dialogue {
		return ""
	}
	if s.Stage == app.Clarify {
		return "reply-start"
	}
	if s.Replying && s.Stage == app.Recording {
		return "reply-stop"
	}
	return ""
}

func sourceText(s app.State) string { return sourceTextForLocale(s, i18n.ZhCN) }

func sourceTextForLocale(s app.State, locale i18n.Locale) string {
	var text strings.Builder
	for _, source := range s.Sources {
		text.WriteString(source.Title)
		if source.PublishedDate != "" {
			if locale == i18n.ZhCN {
				text.WriteString("（" + source.PublishedDate + "）")
			} else {
				text.WriteString(" (" + source.PublishedDate + ")")
			}
		}
		text.WriteString("\n" + source.URL + "\n\n")
	}
	return strings.TrimSpace(text.String())
}
