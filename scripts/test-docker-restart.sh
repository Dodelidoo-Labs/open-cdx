#!/bin/sh
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
COMPOSE_FILE=${OPENCODEX_COMPOSE_FILE:-"$REPO_ROOT/docker/compose.dev.yml"}
BASE_URL=${OPENCODEX_TEST_BASE_URL:-http://127.0.0.1:8080}
DOCKER_COMMAND=${OPENCODEX_DOCKER_COMMAND:-docker}
TASK_TEMP=$(mktemp -d "${TMPDIR:-/tmp}/opencdx-restart.XXXXXX")
trap 'rm -rf "$TASK_TEMP"' EXIT INT TERM

"$DOCKER_COMMAND" compose -f "$COMPOSE_FILE" up -d --build

curl --fail --silent --show-error \
  -H 'Content-Type: application/json' \
  --data '{"name":"Docker restart recovery probe"}' \
  "$BASE_URL/api/v1/enroll" > "$TASK_TEMP/enrollment.json"

DEVICE_ID=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["device_id"])' "$TASK_TEMP/enrollment.json")
ENROLLMENT_SECRET=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["enrollment_secret"])' "$TASK_TEMP/enrollment.json")

# `down` removes the service container and network but deliberately retains the
# named SQLite volume. The test fails if the enrollment cannot be read back.
"$DOCKER_COMMAND" compose -f "$COMPOSE_FILE" down
"$DOCKER_COMMAND" compose -f "$COMPOSE_FILE" up -d

attempt=0
until curl --fail --silent --show-error "$BASE_URL/readyz" > /dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then
    echo "router did not become ready after container recreation" >&2
    exit 1
  fi
  sleep 1
done

STATUS_CODE=$(curl --silent --show-error \
  -o "$TASK_TEMP/status.json" \
  -w '%{http_code}' \
  -H 'Content-Type: application/json' \
  --data "{\"device_id\":\"$DEVICE_ID\",\"enrollment_secret\":\"$ENROLLMENT_SECRET\"}" \
  "$BASE_URL/api/v1/enroll/status")

if [ "$STATUS_CODE" != "202" ]; then
  echo "persisted enrollment returned HTTP $STATUS_CODE instead of 202" >&2
  exit 1
fi

python3 -c 'import json,sys; value=json.load(open(sys.argv[2])); assert value["device_id"] == sys.argv[1] and value["status"] == "pending"' "$DEVICE_ID" "$TASK_TEMP/status.json"
echo "Docker restart recovery passed: enrollment survived full container recreation."
