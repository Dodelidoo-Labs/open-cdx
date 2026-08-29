#!/bin/sh
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SOURCE_APP="$REPO_ROOT/dist/OpenCDX Router.app"
TARGET_PARENT="$HOME/Applications"
TARGET_APP="$TARGET_PARENT/OpenCDX Router.app"
TARGET_HELPER="$TARGET_APP/Contents/Resources/router-helper"

[ -d "$SOURCE_APP" ] || { echo "Build the app first with scripts/build-macos-app.sh" >&2; exit 1; }
mkdir -p "$TARGET_PARENT"

if pgrep -x "OpenCDX Router" >/dev/null 2>&1; then
  osascript -e 'tell application id "com.opencdx.router-menu" to quit' >/dev/null 2>&1 || true
  attempt=0
  while pgrep -x "OpenCDX Router" >/dev/null 2>&1 && [ "$attempt" -lt 30 ]; do
    attempt=$((attempt + 1))
    sleep 0.1
  done
fi

# The menu app starts the helper as a child process, but macOS can leave that
# daemon running after replacing or terminating the UI. Stop the installed
# helper before copying a new bundle so the new binary can bind its loopback
# port on first launch.
if [ -x "$TARGET_HELPER" ]; then
  "$TARGET_HELPER" quit >/dev/null 2>&1 || true
  attempt=0
  while pgrep -f "$TARGET_HELPER daemon" >/dev/null 2>&1 && [ "$attempt" -lt 30 ]; do
    attempt=$((attempt + 1))
    sleep 0.1
  done
fi

ditto "$SOURCE_APP" "$TARGET_APP"
open "$TARGET_APP"
echo "Installed $TARGET_APP"
