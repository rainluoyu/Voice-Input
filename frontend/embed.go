//go:build windows

package frontend

import "embed"

// Build with npm ci && npm run build before compiling the Windows executable.
//
//go:embed all:dist
var Assets embed.FS
