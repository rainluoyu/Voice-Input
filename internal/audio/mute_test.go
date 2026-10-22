package audio

import (
	"errors"
	"reflect"
	"testing"

	"voice_input/internal/i18n"
)

func requireAudioError(t *testing.T, err error, code string) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	var descriptor i18n.Descriptor
	if !errors.As(err, &descriptor) || descriptor.I18nMessage().Code != code {
		t.Fatalf("got error %#v, want %s", err, code)
	}
}

func TestMuteSaveAndRestore(t *testing.T) {
	failed := errors.New("endpoint failed")
	for _, test := range []struct {
		name           string
		original       bool
		getErr         error
		setErr         error
		restoreErr     error
		wantMuteErr    string
		wantRestoreErr string
		wantSets       []bool
	}{
		{name: "unmuted", wantSets: []bool{true, false}},
		{name: "already-muted", original: true, wantSets: []bool{true, true}},
		{name: "read-failed", getErr: failed, wantMuteErr: "audio.mute_failed"},
		{name: "mute-failed", setErr: failed, wantMuteErr: "audio.mute_failed", wantSets: []bool{true, false}},
		{name: "restore-failed", restoreErr: failed, wantRestoreErr: "audio.restore_failed", wantSets: []bool{true, false}},
		{name: "both-failed", setErr: failed, restoreErr: failed, wantMuteErr: "audio.mute_failed", wantRestoreErr: "audio.restore_failed", wantSets: []bool{true, false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			muted := test.original
			var sets []bool
			var state muteState
			err := state.mute(func() (bool, error) {
				return muted, test.getErr
			}, func(value bool) error {
				sets = append(sets, value)
				// Model a SetMute that changes state even when it reports failure.
				muted = value
				if len(sets) == 1 {
					return test.setErr
				}
				return test.restoreErr
			})
			requireAudioError(t, err, test.wantMuteErr)
			requireAudioError(t, state.restore(), test.wantRestoreErr)
			// Cleanup is idempotent, including when the first restore fails.
			requireAudioError(t, state.restore(), "")
			if !reflect.DeepEqual(sets, test.wantSets) {
				t.Fatalf("SetMute calls %v, want %v", sets, test.wantSets)
			}
			if muted != test.original {
				t.Fatalf("mute state %v, want original %v", muted, test.original)
			}
		})
	}
}

func TestNewMuteDefault(t *testing.T) {
	if New().mutePlayback || NewWithMute(false).mutePlayback || !NewWithMute(true).mutePlayback {
		t.Fatal("playback muting must be opt-in")
	}
}
