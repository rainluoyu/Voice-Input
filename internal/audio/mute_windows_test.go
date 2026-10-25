//go:build windows && amd64

package audio

import (
	"fmt"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"github.com/moutend/go-wca/pkg/wca"
)

func TestEndpointSetMute(t *testing.T) {
	for _, test := range []struct {
		name string
		hr   uintptr
		code string
	}{
		{name: "ok"},
		{name: "unchanged", hr: 1},
		{name: "positive-success", hr: 2},
		{name: "failed", hr: 0x80004005, code: "audio.mute_failed"},
	} {
		for _, muted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/muted=%t", test.name, muted), func(t *testing.T) {
				var got [3]uintptr
				var calls int
				vtable := wca.IAudioEndpointVolumeVtbl{
					SetMute: syscall.NewCallback(func(this, value, eventContext uintptr) uintptr {
						got = [3]uintptr{this, value, eventContext}
						calls++
						return test.hr
					}),
				}
				volume := &wca.IAudioEndpointVolume{}
				volume.RawVTable = (*interface{})(unsafe.Pointer(&vtable))

				requireAudioError(t, endpointSetMute(volume, muted), test.code)
				var value uintptr
				if muted {
					value = 1
				}
				want := [3]uintptr{uintptr(unsafe.Pointer(volume)), value, 0}
				if calls != 1 || got != want {
					t.Fatalf("SetMute calls %d with %#v, want one call with %#v", calls, got, want)
				}
				runtime.KeepAlive(volume)
			})
		}
	}
}

func TestEndpointMuteSaveAndRestore(t *testing.T) {
	for _, original := range []uint32{0, 1} {
		t.Run(fmt.Sprintf("original=%d", original), func(t *testing.T) {
			muted := original
			var sets []uintptr
			vtable := wca.IAudioEndpointVolumeVtbl{
				GetMute: syscall.NewCallback(func(this uintptr, value *uint32) uintptr {
					*value = muted
					return 0
				}),
				SetMute: syscall.NewCallback(func(this, value, eventContext uintptr) uintptr {
					sets = append(sets, value)
					// An unchanged mute state is a successful no-op (S_FALSE).
					if muted == uint32(value) {
						return 1
					}
					muted = uint32(value)
					return 0
				}),
			}
			volume := &wca.IAudioEndpointVolume{}
			volume.RawVTable = (*interface{})(unsafe.Pointer(&vtable))
			var state muteState
			requireAudioError(t, state.mute(func() (bool, error) {
				return endpointMute(volume)
			}, func(value bool) error {
				return endpointSetMute(volume, value)
			}), "")
			if muted != 1 {
				t.Fatal("playback was not muted")
			}
			requireAudioError(t, state.restore(), "")
			requireAudioError(t, state.restore(), "")
			if muted != original || len(sets) != 2 || sets[0] != 1 || sets[1] != uintptr(original) {
				t.Fatalf("mute state %d, SetMute calls %v, want original %d and calls [1 %d]", muted, sets, original, original)
			}
			runtime.KeepAlive(volume)
		})
	}
}
