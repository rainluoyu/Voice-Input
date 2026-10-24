package tray

import "testing"

func TestStandardMouseNotifications(t *testing.T) {
	const id = 1
	for _, version4 := range []bool{false, true} {
		for _, tt := range []struct {
			name  string
			event uintptr
			want  notificationAction
		}{
			{"left_button_up", leftButtonUp, activate},
			{"right_button_up", rightButtonUp, openMenu},
			{"context_menu", contextMenu, openMenu},
		} {
			name := "legacy/" + tt.name
			wp, lp := uintptr(id), tt.event
			if version4 {
				name = "v4/" + tt.name
				wp, lp = 123, uintptr(id<<16)|tt.event
			}
			t.Run(name, func(t *testing.T) {
				if got := notificationFor(id, wp, lp); got != tt.want {
					t.Fatalf("event %#x: got %v, want %v", tt.event, got, tt.want)
				}
			})
		}
	}
}

func TestKeyboardNotifications(t *testing.T) {
	for _, event := range []uintptr{selectNotification, keySelectNotification} {
		for _, payload := range [][2]uintptr{{1, event}, {123, 1<<16 | event}} {
			if got := notificationFor(1, payload[0], payload[1]); got != activate {
				t.Fatalf("keyboard event %#x, payload %v: got %v", event, payload, got)
			}
		}
	}
}

func TestNotificationRejectsForeignIconsAndUnknownEvents(t *testing.T) {
	for _, tt := range []struct {
		name   string
		wp, lp uintptr
	}{
		{"foreign_legacy", 2, leftButtonUp},
		{"foreign_v4", 123, 2<<16 | leftButtonUp},
		{"foreign_v4_with_matching_wparam", 1, 2<<16 | leftButtonUp},
		{"zero_identity", 0, leftButtonUp},
		{"unknown_legacy_event", 1, 0x0200},
		{"unknown_v4_event", 123, 1<<16 | 0x0200},
		{"button_down_is_not_activation", 123, 1<<16 | 0x0201},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := notificationFor(1, tt.wp, tt.lp); got != noAction {
				t.Fatalf("foreign or unknown notification was accepted: %v", got)
			}
		})
	}
}
