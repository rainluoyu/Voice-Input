package instance

import (
	"errors"
	"testing"
)

func TestLockResultAndHandleLifetime(t *testing.T) {
	failure := errors.New("access denied")
	for _, tt := range []struct {
		name   string
		handle uintptr
		exists bool
		err    error
		want   error
		closes int
	}{
		{"first", 12, false, nil, nil, 1},
		{"duplicate", 13, true, errors.New("already exists"), ErrAlreadyRunning, 1},
		{"failure", 0, false, failure, failure, 0},
		{"failure_with_handle", 14, false, failure, failure, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			closed := 0
			lock, err := acquire(func() (uintptr, bool, error) { return tt.handle, tt.exists, tt.err }, func(h uintptr) error {
				if h != tt.handle {
					t.Fatal("wrong handle")
				}
				closed++
				return nil
			})
			if !errors.Is(err, tt.want) {
				t.Fatal(err)
			}
			if lock != nil {
				if closed != 0 {
					t.Fatal("first-instance handle closed too early")
				}
				if lock.Close() != nil || lock.Close() != nil {
					t.Fatal("close failed")
				}
			}
			if closed != tt.closes {
				t.Fatal("handle leaked or closed twice", closed)
			}
		})
	}
	if lock, err := acquire(func() (uintptr, bool, error) { return 0, false, nil }, func(uintptr) error { t.Fatal("closed invalid handle"); return nil }); lock != nil || err == nil {
		t.Fatal("invalid handle accepted")
	}
}
