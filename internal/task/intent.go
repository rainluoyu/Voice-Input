package task

type Intent string

const (
	Dictation Intent = "dictation"
	Dialogue  Intent = "dialogue"
)

func (i Intent) Valid() bool { return i == Dictation || i == Dialogue }

func (i Intent) Label() string {
	if i == Dialogue {
		return "AI 对话"
	}
	return "语音润色"
}
