package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
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
		return errors.New("已达到 8 轮补充上限，请关闭后重新开始任务")
	}
	total := len(c.Original)
	validText := func(s string, limit int) bool {
		return strings.TrimSpace(s) != "" && len(s) <= limit && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
	}
	if !validText(c.Original, 32000) {
		return errors.New("原始语音文本无效或过长，请重新开始任务")
	}
	for _, turn := range c.Clarifications {
		if !validText(turn.Question, 2000) || !validText(turn.Answer, 32000) {
			return errors.New("补充内容无效或过长，请重新录制")
		}
		total += len(turn.Question) + len(turn.Answer)
	}
	if total > 128<<10 {
		return errors.New("补充对话过长，请关闭后重新开始任务")
	}
	return nil
}

func (c Conversation) Transcript() string {
	if len(c.Clarifications) == 0 {
		return c.Original
	}
	var text strings.Builder
	text.WriteString("原始语音：" + c.Original)
	for i, turn := range c.Clarifications {
		fmt.Fprintf(&text, "\n\n问题 %d：%s\n补充 %d：%s", i+1, turn.Question, i+1, turn.Answer)
	}
	return text.String()
}
