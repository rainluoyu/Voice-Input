#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ -n "${PROXY_URL:-}" ]]; then
 export HTTPS_PROXY="$PROXY_URL" HTTP_PROXY="$PROXY_URL"
 export npm_config_proxy="$PROXY_URL" npm_config_https_proxy="$PROXY_URL"
fi
if [[ ! -f bin/voice_input.exe ]]; then echo 'Build with scripts/check.sh first.' >&2; exit 1; fi
npm --prefix frontend audit
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -mode=binary bin/voice_input.exe
