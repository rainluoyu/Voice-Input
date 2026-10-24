#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if (( $# > 1 )); then
 printf 'Usage: %s [version]\n' "$0" >&2
 exit 2
fi
if (( $# == 1 )); then
 version="$(go run ./tools/winresource -version "$1" -print-version)"
else
 version="$(go run ./tools/winresource -print-version)"
fi
npm --prefix frontend ci
npm --prefix frontend run build
go run ./tools/winresource -version "$version"
mkdir -p bin
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-H=windowsgui -X=voice_input/internal/buildmeta.Version=$version" -o bin/voice_input.exe ./cmd/voice_input
go run ./tools/package -version "$version"
