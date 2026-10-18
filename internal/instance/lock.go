package instance

import (
	"errors"
	"sync"
)

var ErrAlreadyRunning = errors.New("application is already running")

type Lock struct {
	handle uintptr
	close  func(uintptr) error
	once   sync.Once
	err    error
}

func (l *Lock) Close() error {
	l.once.Do(func() { l.err = l.close(l.handle) })
	return l.err
}

func acquire(create func() (uintptr, bool, error), close func(uintptr) error) (*Lock, error) {
	handle, exists, err := create()
	if exists || err != nil {
		if handle != 0 {
			_ = close(handle)
		}
		if exists {
			return nil, ErrAlreadyRunning
		}
		return nil, err
	}
	if handle == 0 {
		return nil, errors.New("single-instance lock returned an invalid handle")
	}
	return &Lock{handle: handle, close: close}, nil
}
