package tray

const (
	leftButtonUp          = 0x0202
	rightButtonUp         = 0x0205
	contextMenu           = 0x007b
	selectNotification    = 0x0400
	keySelectNotification = 0x0401
)

type notificationAction uint8

const (
	noAction notificationAction = iota
	activate
	openMenu
)

func notificationFor(id uint32, wp, lp uintptr) notificationAction {
	// Identify the wire format rather than relying on a cached SETVERSION result.
	payload := uint32(lp)
	switch {
	case payload>>16 == id:
		payload &= 0xffff // v4: low word is the event, high word is the icon ID.
	case payload>>16 == 0 && wp == uintptr(id) && lp == uintptr(payload):
		// Legacy: wParam is the icon ID and lParam is the unpacked event.
	default:
		return noAction
	}
	switch payload {
	case leftButtonUp, selectNotification, keySelectNotification:
		return activate
	case rightButtonUp, contextMenu:
		return openMenu
	}
	return noAction
}
