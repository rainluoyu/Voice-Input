package popup

import "voice_input/internal/app"

// A result is followed only once. Revisions also change for meter and mouse
// events, so they are deliberately not part of the result identity.
type resultFollow struct {
	task    string
	turn    uint64
	text    string
	pending bool
}

func (f *resultFollow) observe(s app.State) {
	if f.task != s.TaskID || f.turn != s.Turn {
		*f = resultFollow{task: s.TaskID, turn: s.Turn}
	}
	if !s.Visible || (s.Stage != app.Done && s.Stage != app.Error) || s.Text == "" {
		f.pending = false
		return
	}
	if s.Text != f.text {
		f.text, f.pending = s.Text, true
	}
}

func (f *resultFollow) cancel() { f.pending = false }

func (f *resultFollow) target(resultBottom, viewportHeight, maxScroll int32) (int32, bool) {
	if !f.pending {
		return 0, false
	}
	f.pending = false
	return max(0, min(max(0, maxScroll), resultBottom-viewportHeight)), true
}
