// Package buildmeta defines the version shared by release tools and the app.
package buildmeta

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is overridden by release builds using -X=voice_input/internal/buildmeta.Version.
var Version = "0.3.0"

// ParsedVersion keeps the canonical SemVer and its Windows numeric counterpart.
type ParsedVersion struct {
	Canonical string
	Windows   [4]uint16
}

// Parse accepts bare stable/prerelease SemVer, without a v prefix or build metadata.
// Core components must fit Windows' uint16 version fields.
func Parse(value string) (ParsedVersion, error) {
	invalid := func() (ParsedVersion, error) {
		return ParsedVersion{}, fmt.Errorf("invalid release version %q: want bare SemVer with uint16 core components", value)
	}
	core, prerelease, hasPrerelease := strings.Cut(value, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return invalid()
	}
	parsed := ParsedVersion{Canonical: value}
	for i, part := range parts {
		if !digits(part) || len(part) > 1 && part[0] == '0' {
			return invalid()
		}
		n, err := strconv.ParseUint(part, 10, 16)
		if err != nil {
			return invalid()
		}
		parsed.Windows[i] = uint16(n)
	}
	if hasPrerelease {
		for _, identifier := range strings.Split(prerelease, ".") {
			if identifier == "" || digits(identifier) && len(identifier) > 1 && identifier[0] == '0' {
				return invalid()
			}
			for _, c := range identifier {
				if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '-') {
					return invalid()
				}
			}
		}
	}
	return parsed, nil
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
