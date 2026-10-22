package pipeline

import (
	"context"
	"testing"
	"time"

	"voice_input/internal/app"
	"voice_input/internal/audio"
	"voice_input/internal/i18n"
)

func TestFactorySnapshotsGeneralSettings(t *testing.T) {
	cfg := liveConfig()
	cfg.General.AutoCopy = true
	cfg.General.MuteWhileRecording = true
	cfg.Popup.AutoHideSeconds = 75
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor, options, err := New(ctx, "general", app.Dictation, cfg, keyVault{"asr": "test-asr", "llm": "test-llm"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !options.AutoCopy || options.AutoHide != 75*time.Second {
		t.Fatal("task options did not snapshot general settings", options)
	}
	cfg.General.AutoCopy = false
	cfg.General.MuteWhileRecording = false
	cfg.Popup.AutoHideSeconds = 5
	session := processor.(*Session)
	if !session.Config.General.AutoCopy || !session.Config.General.MuteWhileRecording || session.Config.Popup.AutoHideSeconds != 75 {
		t.Fatal("active session changed with later configuration")
	}
}

type muteFailureRecorder struct {
	fakeRecorder
	err error
}

func (r muteFailureRecorder) Start(context.Context, string, int, func(float64)) error { return r.err }
func (r muteFailureRecorder) Stop(context.Context) (audio.Data, error)                { return audio.Data{}, r.err }

func TestMuteFailuresRetainSafeDescriptor(t *testing.T) {
	for _, code := range []string{"audio.mute_failed", "audio.restore_failed"} {
		session := Session{Recorder: muteFailureRecorder{err: i18n.Error(code)}}
		if err := session.Begin(context.Background()); i18n.FromError(err, "app.microphone_failed").Code != code {
			t.Fatal("Begin hid playback failure", err)
		}
		if _, err := session.Transcribe(context.Background()); i18n.FromError(err, "app.asr_failed").Code != code {
			t.Fatal("Stop hid playback failure", err)
		}
	}
}
