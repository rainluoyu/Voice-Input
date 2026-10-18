//go:build !windows

package settings

import "os"

// The shared writer removes the temporary name after publishing the link.
func publishNoReplace(temp, target string) error { return os.Link(temp, target) }
