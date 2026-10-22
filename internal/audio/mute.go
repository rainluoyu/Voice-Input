package audio

import "voice_input/internal/i18n"

// muteState is confined to the capture thread, including its restore callback.
type muteState struct {
	undo func() error
}

func (m *muteState) mute(get func() (bool, error), set func(bool) error) error {
	original, err := get()
	if err != nil {
		return i18n.Error("audio.mute_failed")
	}
	// Even a failed SetMute may have changed the endpoint; restore on that path too.
	m.undo = func() error { return set(original) }
	if err := set(true); err != nil {
		return i18n.Error("audio.mute_failed")
	}
	return nil
}

func (m *muteState) restore() error {
	if m.undo == nil {
		return nil
	}
	undo := m.undo
	m.undo = nil
	if err := undo(); err != nil {
		return i18n.Error("audio.restore_failed")
	}
	return nil
}
