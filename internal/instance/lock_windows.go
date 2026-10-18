//go:build windows

package instance

import (
	"errors"

	"golang.org/x/sys/windows"
)

func Acquire() (*Lock, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return acquireName(`Local\voice_input.` + user.User.Sid.String() + ".instance")
}

func acquireName(name string) (*Lock, error) {
	value, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	return acquire(func() (uintptr, bool, error) {
		handle, err := windows.CreateMutex(nil, false, value)
		return uintptr(handle), errors.Is(err, windows.ERROR_ALREADY_EXISTS), err
	}, func(handle uintptr) error { return windows.CloseHandle(windows.Handle(handle)) })
}
