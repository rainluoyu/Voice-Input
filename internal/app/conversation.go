package app

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	"voice_input/internal/i18n"
)

const MaxClarifications = 8

// Conversation is scoped to one task, not to a recorder or a processing turn.
type Conversation struct {
	Original       string
	Clarifications []Clarification
}

type Clarification struct {
	Question string
	Answer   string
}

// ConversationProcessor is optional so ASR pass-through and simple processors
// do not need to implement a dialogue protocol they never use.
type ConversationProcessor interface {
	CompleteConversation(context.Context, Conversation) (Result, error)
}

func (c Conversation) Clone() Conversation {
	c.Clarifications = append([]Clarification(nil), c.Clarifications...)
	return c
}

func (c Conversation) Validate() error {
	if len(c.Clarifications) > MaxClarifications {
		return i18n.Error("app.clarification_limit")
	}
	total := len(c.Original)
	validText := func(s string, limit int) bool {
		return strings.TrimSpace(s) != "" && len(s) <= limit && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
	}
	if !validText(c.Original, 32000) {
		return i18n.Error("app.original_invalid")
	}
	for _, turn := range c.Clarifications {
		if !validText(turn.Question, 2000) || !validText(turn.Answer, 32000) {
			return i18n.Error("app.answer_invalid")
		}
		total += len(turn.Question) + len(turn.Answer)
	}
	if total > 128<<10 {
		return i18n.Error("app.conversation_large")
	}
	return nil
}

func (c Conversation) Transcript() string {
	return c.TranscriptForLocale(i18n.ZhCN)
}

func (c Conversation) TranscriptForLocale(locale i18n.Locale) string {
	if len(c.Clarifications) == 0 {
		return c.Original
	}
	var text strings.Builder
	text.WriteString(i18n.M("transcript.original", "text", c.Original).Text(locale))
	for i, turn := range c.Clarifications {
		text.WriteString(i18n.M("transcript.turn", "number", strconv.Itoa(i+1), "question", turn.Question, "answer", turn.Answer).Text(locale))
	}
	return text.String()
}
