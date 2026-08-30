#!/bin/sh
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SOURCE_APP="$REPO_ROOT/dist/OpenCDX Router.app"
TARGET_PARENT="$HOME/Applications"
TARGET_APP="$TARGET_PARENT/OpenCDX Router.app"
TARGET_HELPER="$TARGET_APP/Contents/Resources/router-helper"
EXPECTED_BUNDLE_ID="com.opencdx.router-menu"

[ -d "$SOURCE_APP" ] || { echo "Build the app first with scripts/build-macos-app.sh" >&2; exit 1; }
mkdir -p "$TARGET_PARENT"

codesign --verify --deep --strict --verbose=2 "$SOURCE_APP"
BUNDLE_ID=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$SOURCE_APP/Contents/Info.plist")
if [ "$BUNDLE_ID" != "$EXPECTED_BUNDLE_ID" ]; then
  echo "Refusing to install unexpected bundle identifier: $BUNDLE_ID" >&2
  exit 1
fi
TEAM_ID=$(codesign -d --verbose=4 "$SOURCE_APP" 2>&1 | sed -n 's/^TeamIdentifier=//p')
if [ -z "$TEAM_ID" ] || [ "$TEAM_ID" = "not set" ]; then
  echo "Refusing to install an ad-hoc-signed app because every rebuild can create another Local Network privacy entry." >&2
  echo "Build with a stable Apple-issued code-signing identity." >&2
  exit 1
fi
NEW_REQUIREMENT=$(codesign -d -r- "$SOURCE_APP" 2>&1 | sed -n 's/^designated => //p')
if [ -z "$NEW_REQUIREMENT" ]; then
  echo "Could not determine the new app's designated requirement." >&2
  exit 1
fi
if [ -d "$TARGET_APP" ]; then
  OLD_REQUIREMENT=$(codesign -d -r- "$TARGET_APP" 2>&1 | sed -n 's/^designated => //p')
  if [ "$OLD_REQUIREMENT" != "$NEW_REQUIREMENT" ] && [ "${OPENCODEX_ALLOW_SIGNING_IDENTITY_CHANGE:-0}" != "1" ]; then
    echo "Refusing to replace the installed app with a different signing identity." >&2
    echo "A signer change creates another macOS Local Network privacy identity. Rebuild with the previous identity, or set OPENCODEX_ALLOW_SIGNING_IDENTITY_CHANGE=1 only for an intentional migration." >&2
    exit 1
  fi
fi

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

if pgrep -x "OpenCDX Router" >/dev/null 2>&1 || pgrep -f "$TARGET_HELPER daemon" >/dev/null 2>&1; then
  echo "The installed app or helper did not stop; refusing to replace a running bundle." >&2
  exit 1
fi

INSTALL_STAGING=$(mktemp -d "$TARGET_PARENT/.opencdx-install.XXXXXX")
PREVIOUS_BUNDLE="$INSTALL_STAGING/previous-bundle"
INSTALL_COMPLETE=0
rollback_install() {
  if [ "$INSTALL_COMPLETE" != "1" ]; then
    if [ -d "$TARGET_APP" ] && [ ! -d "$SOURCE_APP" ]; then
      mv "$TARGET_APP" "$SOURCE_APP"
    fi
    if [ -d "$PREVIOUS_BUNDLE" ] && [ ! -d "$TARGET_APP" ]; then
      mv "$PREVIOUS_BUNDLE" "$TARGET_APP"
    fi
  fi
  rm -rf "$INSTALL_STAGING"
}
trap rollback_install EXIT
trap 'exit 1' INT TERM

if [ -d "$TARGET_APP" ]; then
  mv "$TARGET_APP" "$PREVIOUS_BUNDLE"
fi
mv "$SOURCE_APP" "$TARGET_APP"
codesign --verify --deep --strict --verbose=2 "$TARGET_APP"
open "$TARGET_APP"
INSTALL_COMPLETE=1
rm -rf "$INSTALL_STAGING"
trap - EXIT INT TERM
echo "Installed $TARGET_APP with stable team identity $TEAM_ID. The dist bundle was consumed so only one app copy remains."
