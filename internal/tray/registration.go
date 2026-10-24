package tray

const retryLimit = 30

type operation uintptr

const (
	add        operation = 0
	modify     operation = 1
	remove     operation = 2
	setVersion operation = 4
)

type recovery uint8

const (
	ready recovery = iota
	retry
	unavailable
	stopped
)

// Registration decisions are independent of HWNDs and run on the UI thread.
type registration struct {
	notify   func(operation) bool
	failures int
	added    bool
	closed   bool
}

func (r *registration) restore() recovery {
	if r.closed {
		return stopped
	}
	// A duplicate TaskbarCreated must not add a second icon or delete a live one.
	if r.notify(modify) {
		r.added, r.failures = true, 0
		return ready
	}
	r.added = false
	if r.notify(add) {
		r.added, r.failures = true, 0
		// SETVERSION failure leaves a valid icon; both callback formats are accepted.
		r.notify(setVersion)
		return ready
	}
	r.failures++
	if r.failures >= retryLimit {
		return unavailable
	}
	return retry
}

func (r *registration) close() {
	if r.closed {
		return
	}
	r.closed = true
	if r.notify != nil {
		r.notify(remove)
	}
	r.added = false
}
