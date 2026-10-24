#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export NO_PROXY="127.0.0.1,localhost,::1${NO_PROXY:+,$NO_PROXY}"
export UI_TEST_PORT="${UI_TEST_PORT:-5173}"
tools="${TMPDIR:-/tmp}/voice_input-ui-tools"
if [[ ! -f "$tools/pyproject.toml" ]]; then uv init --bare --name voice-input-ui-tools "$tools"; fi
uv add --project "$tools" 'playwright==1.64.0'
uv run --project "$tools" playwright install chromium --only-shell
npm --prefix frontend run build
node frontend/node_modules/vite/bin/vite.js preview frontend --host 127.0.0.1 --port "$UI_TEST_PORT" --strictPort > /tmp/voice_input-vite.log 2>&1 &
pid=$!
cleanup(){ kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; }
trap cleanup EXIT
ready=false
for _ in {1..80}; do
 if ! kill -0 "$pid" 2>/dev/null; then echo 'Vite preview exited; check /tmp/voice_input-vite.log' >&2; exit 1; fi
 if curl --noproxy '*' -fsS "http://127.0.0.1:$UI_TEST_PORT/" >/dev/null 2>&1; then ready=true; break; fi
 sleep .1
done
if [[ "$ready" != true ]]; then echo 'Vite preview not ready' >&2; exit 1; fi
uv run --project "$tools" tests/frontend_smoke.py
uv run --project "$tools" tests/frontend_bridge.py
uv run --project "$tools" tests/frontend_settings.py
