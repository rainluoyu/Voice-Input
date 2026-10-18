package app

import (
	"context"
	"errors"
	"time"
)

// Mock never accesses a microphone, network, filesystem, or model API.
type Mock struct {
	Intent   Intent
	Scenario string
	Delay    time.Duration
	RawText  string // Optional deterministic transcript for offline dialogue fixtures.
}

func (m Mock) wait(ctx context.Context) error {
	timer := time.NewTimer(m.Delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (m Mock) Transcribe(ctx context.Context) (string, error) {
	if err := m.wait(ctx); err != nil {
		return "", err
	}
	if m.RawText != "" {
		return m.RawText, nil
	}
	return "额，小李啊，帮我去那个一区菜鸟驿站取个快递，号一会发你。", nil
}
func (m Mock) Complete(ctx context.Context, raw string) (Result, error) {
	if err := m.wait(ctx); err != nil {
		return Result{}, err
	}
	if m.Intent == Dictation && (m.Scenario == "clarify" || m.Scenario == "insert" || m.Scenario == "") {
		text := "小李，帮我去一区菜鸟驿站取个快递，取件号一会儿发你。📦"
		if m.RawText != "" {
			text = raw
		}
		return Result{Status: "insert", Text: text}, nil
	}
	if m.Intent == Dialogue && (m.Scenario == "insert" || m.Scenario == "") {
		return Result{Status: "insert", Text: "离线 AI 对话模拟结果。\n\n此内容仅用于演示多行输入，未调用真实模型。"}, nil
	}
	switch m.Scenario {
	case "skip":
		return Result{Status: "skip", Notice: "Mock 条件不成立，无需输出"}, nil
	case "clarify":
		return Result{Status: "clarify", Notice: "Mock 澄清：你指的是哪个城市？"}, nil
	case "error":
		return Result{}, errors.New("mock failure")
	default:
		return Result{Status: "insert", Text: "小李，帮我去一区菜鸟驿站取个快递，取件号一会儿发你。📦"}, nil
	}
}

func (m Mock) CompleteConversation(ctx context.Context, conversation Conversation) (Result, error) {
	if err := conversation.Validate(); err != nil {
		return Result{}, err
	}
	if m.Scenario != "clarify" || len(conversation.Clarifications) == 0 {
		return m.Complete(ctx, conversation.Original)
	}
	if err := m.wait(ctx); err != nil {
		return Result{}, err
	}
	last := conversation.Clarifications[len(conversation.Clarifications)-1]
	return Result{Status: "insert", Text: "模拟续答：" + conversation.Original + "；补充：" + last.Answer}, nil
}
