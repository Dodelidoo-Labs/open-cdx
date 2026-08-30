#!/bin/sh
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
HELPER_BINARY=${OPENCODEX_HELPER_BINARY:-"$REPO_ROOT/bin/router-helper"}
ROUTER_URL=${OPENCODEX_TEST_ROUTER_URL:-http://127.0.0.1:8080}
ADMIN_TOKEN_FILE=${OPENCODEX_ADMIN_TOKEN_FILE:-"$REPO_ROOT/docker/secrets/admin_token"}
TASK_TEMP=$(mktemp -d "${TMPDIR:-/tmp}/opencdx-helper-e2e.XXXXXX")
HELPER_PID=""

cleanup() {
  if [ -x "$HELPER_BINARY" ] && [ -f "$TASK_TEMP/helper.json" ]; then
    "$HELPER_BINARY" --config "$TASK_TEMP/helper.json" quit >/dev/null 2>&1 || true
  fi
  if [ -n "$HELPER_PID" ]; then
    kill "$HELPER_PID" >/dev/null 2>&1 || true
  fi
  rm -rf "$TASK_TEMP"
}
trap cleanup EXIT INT TERM

[ -x "$HELPER_BINARY" ] || { echo "set OPENCODEX_HELPER_BINARY to a router-helper executable" >&2; exit 1; }
[ -r "$ADMIN_TOKEN_FILE" ] || { echo "administrator token file is not readable" >&2; exit 1; }
export XDG_CONFIG_HOME="$TASK_TEMP/xdg"
python3 -c 'import pathlib,sys; sys.stdout.write(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8").strip())' \
  "$ADMIN_TOKEN_FILE" > "$TASK_TEMP/admin-token"
chmod 600 "$TASK_TEMP/admin-token"

"$HELPER_BINARY" --config "$TASK_TEMP/helper.json" enroll \
  --router "$ROUTER_URL" --name "Helper end-to-end probe" --no-wait >/dev/null

DEVICE_ID=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["device_id"])' "$TASK_TEMP/helper.json")
curl --fail --silent --show-error -c "$TASK_TEMP/cookies" -o /dev/null \
  --data-urlencode "token@$TASK_TEMP/admin-token" "$ROUTER_URL/admin/login"
curl --fail --silent --show-error -b "$TASK_TEMP/cookies" \
  -o "$TASK_TEMP/dashboard.html" "$ROUTER_URL/admin"
CSRF=$(python3 -c 'import re,sys; value=open(sys.argv[1], encoding="utf-8").read(); match=re.search(r"name=\"csrf\" value=\"([^\"]+)\"", value); assert match; print(match.group(1))' "$TASK_TEMP/dashboard.html")

curl --fail --silent --show-error -b "$TASK_TEMP/cookies" -o /dev/null \
  --data-urlencode "csrf=$CSRF" "$ROUTER_URL/admin/devices/$DEVICE_ID/approve"
"$HELPER_BINARY" --config "$TASK_TEMP/helper.json" pair --timeout 20s >/dev/null

"$HELPER_BINARY" --config "$TASK_TEMP/helper.json" daemon >"$TASK_TEMP/daemon.log" 2>&1 &
HELPER_PID=$!
attempt=0
until curl --fail --silent --show-error http://127.0.0.1:17464/healthz >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then
    echo "helper daemon did not become ready" >&2
    exit 1
  fi
  sleep 0.2
done

UNAUTHENTICATED=$(curl --silent --output /dev/null --write-out '%{http_code}' \
  -H 'Content-Type: application/json' --data '{}' http://127.0.0.1:17464/v1/responses)
test "$UNAUTHENTICATED" = "401"

LOCAL_TOKEN=$("$HELPER_BINARY" --config "$TASK_TEMP/helper.json" token)
printf 'Authorization: Bearer %s\n' "$LOCAL_TOKEN" > "$TASK_TEMP/auth-header"
chmod 600 "$TASK_TEMP/auth-header"
AUTHENTICATED=$(curl --silent --output /dev/null --write-out '%{http_code}' \
  -H @"$TASK_TEMP/auth-header" -H 'Content-Type: application/json' \
  --data '{}' http://127.0.0.1:17464/v1/responses)
test "$AUTHENTICATED" = "400"

curl --fail --silent --show-error -b "$TASK_TEMP/cookies" -o /dev/null \
  --data-urlencode "csrf=$CSRF" "$ROUTER_URL/admin/devices/$DEVICE_ID/revoke"
REVOKED=$(curl --silent --output /dev/null --write-out '%{http_code}' \
  -H @"$TASK_TEMP/auth-header" -H 'Content-Type: application/json' \
  --data '{}' http://127.0.0.1:17464/v1/responses)
test "$REVOKED" = "401"

"$HELPER_BINARY" --config "$TASK_TEMP/helper.json" quit >/dev/null
wait "$HELPER_PID"
HELPER_PID=""
echo "Helper end-to-end passed: local-auth=401 paired-route=400 removed-route=401."
