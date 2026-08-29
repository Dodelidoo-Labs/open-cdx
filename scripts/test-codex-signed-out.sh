#!/bin/sh
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TASK_TEMP=$(mktemp -d "${TMPDIR:-/tmp}/opencdx-codex-proof.XXXXXX")
TASK_SERVER_PID=""
cleanup() {
  if [ -n "$TASK_SERVER_PID" ]; then kill "$TASK_SERVER_PID" >/dev/null 2>&1 || true; fi
  rm -rf "$TASK_TEMP"
}
trap cleanup EXIT INT TERM

python3 "$REPO_ROOT/scripts/testing/codex_mock_server.py" --port-file "$TASK_TEMP/port" --result-file "$TASK_TEMP/result.json" &
TASK_SERVER_PID=$!
TASK_TRIES=0
while [ ! -s "$TASK_TEMP/port" ]; do
  TASK_TRIES=$((TASK_TRIES + 1))
  [ "$TASK_TRIES" -lt 50 ] || { echo "mock Responses server did not start" >&2; exit 1; }
  sleep 0.1
done
TASK_PORT=$(tr -d '\r\n' < "$TASK_TEMP/port")

mkdir -p "$TASK_TEMP/codex-home"
sed -e "s|__BASE_URL__|http://127.0.0.1:$TASK_PORT/v1|" -e "s|__CATALOG__|$REPO_ROOT/testdata/codex-catalog.json|" \
  "$REPO_ROOT/testdata/signed-out-config.toml.in" > "$TASK_TEMP/codex-home/config.toml"

CODEX_HOME="$TASK_TEMP/codex-home" codex exec --ephemeral --skip-git-repo-check \
  --model openrouter/test-model --cd "$REPO_ROOT" "Reply with no text and finish." > "$TASK_TEMP/codex-output" 2>&1

python3 -c 'import json,sys; value=json.load(open(sys.argv[1])); assert value["authorized"] is True; assert value["path"] == "/v1/responses"' "$TASK_TEMP/result.json"
echo "Codex used command authentication with a fresh auth-free CODEX_HOME."
