#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ -n "${PROXY_URL:-}" ]]; then
 export HTTPS_PROXY="$PROXY_URL" HTTP_PROXY="$PROXY_URL"
 export npm_config_proxy="$PROXY_URL" npm_config_https_proxy="$PROXY_URL"
fi
npm --prefix frontend ci
npm --prefix frontend run build
go run ./tools/winresource
mkdir -p bin
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-H windowsgui' -o bin/voice_input.exe ./cmd/voice_input
go run ./tools/package
