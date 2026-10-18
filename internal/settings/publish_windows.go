//go:build windows

package settings

import "golang.org/x/sys/windows"

func publishNoReplace(temp, target string) error {
	from, err := windows.UTF16PtrFromString(temp)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, 0)
}
