//go:build windows

package startup

import "golang.org/x/sys/windows"

func sameTaskSecurity(actual, expected string) (bool, error) {
	normalize := func(sddl string) (string, error) {
		sd, err := windows.SecurityDescriptorFromString(sddl)
		if err != nil {
			return "", err
		}
		// Task Scheduler adds AI when registering a protected DACL. Clear only
		// that bookkeeping bit in this in-memory copy, not on the actual task.
		if err := sd.SetControl(windows.SE_DACL_AUTO_INHERITED, 0); err != nil {
			return "", err
		}
		return sd.String(), nil
	}
	a, err := normalize(actual)
	if err != nil {
		return false, err
	}
	b, err := normalize(expected)
	return err == nil && a != "" && a == b, err
}
