package app

import (
	"context"

	"voice_input/internal/i18n"
)

// Present projects application-owned notice metadata only. Model content, source
// data, task identity and revision/timer state are never translated or changed.
func Present(state State, locale i18n.Locale) State {
	if state.NoticeMessage != nil {
		state.Notice = state.NoticeMessage.Text(locale)
	}
	return state
}

type taskLocaleKey struct{}

// TaskLocale carries the original task's language through a fresh factory call
// for a clarification turn. A new task uses its configuration snapshot instead.
func TaskLocale(ctx context.Context, fallback i18n.Locale) i18n.Locale {
	if locale, ok := ctx.Value(taskLocaleKey{}).(i18n.Locale); ok {
		return locale
	}
	return fallback
}

func (c *Controller) notice(message i18n.Message) {
	c.state.NoticeMessage = nil
	if message.Code == "" {
		c.state.Notice = ""
		return
	}
	c.state.NoticeMessage = &message
	locale := c.options.Locale
	if locale == "" {
		locale = i18n.ZhCN
	}
	c.state.Notice = message.Text(locale)
}
