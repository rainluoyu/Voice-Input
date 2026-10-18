package popup

import (
	"fmt"
	"strings"
	"time"

	"voice_input/internal/app"
)

func stageLabel(s app.State) string {
	if s.Stage == app.LLM {
		if s.Intent == app.Dialogue {
			return "生成所需文本"
		}
		return "正在润色文本"
	}
	if s.Replying && s.Stage == app.Recording {
		return "正在录制补充"
	}
	if s.Replying && s.Stage == app.ASR {
		return "识别补充语音"
	}
	labels := map[app.Stage]string{
		app.Idle: "准备就绪", app.Recording: "正在录音", app.ASR: "识别语音",
		app.LLM: "整理语义", app.Search: "查证事实", app.Injecting: "正在输入",
		app.Done: "已输入", app.Skip: "无需输出", app.Clarify: "需要补充信息",
		app.Error: "输入未完成", app.Cancelled: "已取消",
	}
	return labels[s.Stage]
}

func purposeLabel(s app.State) string {
	label := s.Intent.Label()
	if s.Mode == "asr_mock_llm" {
		label += " · ASR 直通"
	}
	return label
}

func footer(s app.State, now time.Time) string {
	if s.Stage == app.Clarify {
		return s.Hotkey + " 开始补充"
	}
	if s.Stage == app.Recording {
		if s.Replying {
			return s.Hotkey + " 结束补充"
		}
		return s.Hotkey + " 结束录音"
	}
	if s.Pinned {
		return "保持显示"
	}
	if s.HideAt != 0 {
		seconds := max(0, (s.HideAt-now.UnixMilli()+999)/1000)
		return fmt.Sprintf("%d 秒后隐藏", seconds)
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

func sourceText(s app.State) string {
	var text strings.Builder
	for _, source := range s.Sources {
		text.WriteString(source.Title)
		if source.PublishedDate != "" {
			text.WriteString("（" + source.PublishedDate + "）")
		}
		text.WriteString("\n" + source.URL + "\n\n")
	}
	return strings.TrimSpace(text.String())
}
