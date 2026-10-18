//go:build windows

package instance

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestNativeMutexLifetime(t *testing.T) {
	name := fmt.Sprintf(`Local\voice_input.test.%d.%d`, os.Getpid(), time.Now().UnixNano())
	first, err := acquireName(name)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := acquireName(name); second != nil || !errors.Is(err, ErrAlreadyRunning) {
		if second != nil {
			second.Close()
		}
		t.Fatal("duplicate accepted", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := acquireName(name)
	if err != nil {
		t.Fatal("lock remained after close", err)
	}
	next.Close()
}
