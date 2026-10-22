//go:build !windows

package startup

import "voice_input/internal/i18n"

func nativeBackend() (backend, runValue, error) {
	return nil, runValue{}, i18n.Error("startup.failed")
}

// HandleHelper reserves malformed helper invocations on unsupported platforms as
// well, so they cannot accidentally enter the normal application startup path.
func HandleHelper(args []string) (handled bool, exitCode int) {
	handled, _, _, _ = parseHelper(args)
	if handled {
		return true, exitFailed
	}
	return false, 0
}
