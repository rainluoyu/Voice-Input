#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ ! -f bin/voice_input.exe ]]; then echo 'Build with scripts/check.sh first.' >&2; exit 1; fi
npm --prefix frontend audit
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -mode=binary bin/voice_input.exe
