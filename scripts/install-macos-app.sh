#!/bin/sh
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SOURCE_APP="$REPO_ROOT/dist/OpenCDX Router.app"
SOURCE_HELPER="$SOURCE_APP/Contents/Resources/router-helper"
SYSTEM_APP="/Applications/OpenCDX Router.app"
USER_APP="$HOME/Applications/OpenCDX Router.app"
CHECK_ONLY=0
if [ "${1:-}" = "--check" ]; then
  CHECK_ONLY=1
  shift
fi
if [ "$#" -ne 0 ]; then
  echo "Usage: $0 [--check]" >&2
  exit 2
fi

TARGET_APP=${OPENCODEX_INSTALL_APP_PATH:-}
if [ -z "$TARGET_APP" ]; then
  if [ -d "$SYSTEM_APP" ] && [ -d "$USER_APP" ]; then
    echo "Refusing to choose between two installed OpenCDX Router copies:" >&2
    echo "  $SYSTEM_APP" >&2
    echo "  $USER_APP" >&2
    echo "Remove the unintended copy or set OPENCODEX_INSTALL_APP_PATH explicitly." >&2
    exit 1
  elif [ -d "$SYSTEM_APP" ]; then
    TARGET_APP=$SYSTEM_APP
  elif [ -d "$USER_APP" ]; then
    TARGET_APP=$USER_APP
  else
    TARGET_APP=$USER_APP
  fi
fi
case "$TARGET_APP" in
  /*/"OpenCDX Router.app") ;;
  *)
    echo "OPENCODEX_INSTALL_APP_PATH must be an absolute path ending in OpenCDX Router.app." >&2
    exit 1
    ;;
esac
for KNOWN_APP in "$SYSTEM_APP" "$USER_APP"; do
  if [ -d "$KNOWN_APP" ] && [ "$KNOWN_APP" != "$TARGET_APP" ]; then
    echo "Refusing to install while another OpenCDX Router copy exists at $KNOWN_APP." >&2
    exit 1
  fi
done
TARGET_PARENT=${TARGET_APP%/*}
TARGET_HELPER="$TARGET_APP/Contents/Resources/router-helper"
EXPECTED_BUNDLE_ID="com.dodelidoo.opencdx"
EXPECTED_URL_NAME="com.dodelidoo.opencdx.oauth"
EXPECTED_URL_SCHEME="com.dodelidoo.opencdx"
EXPECTED_HELPER_ID="com.dodelidoo.opencdx.helper"

[ -d "$SOURCE_APP" ] || { echo "Build the app first with scripts/build-macos-app.sh" >&2; exit 1; }
if [ "$CHECK_ONLY" = "1" ]; then
  [ -d "$TARGET_PARENT" ] || { echo "Target parent does not exist: $TARGET_PARENT" >&2; exit 1; }
else
  mkdir -p "$TARGET_PARENT"
fi

codesign --verify --deep --strict --verbose=2 "$SOURCE_APP"
BUNDLE_ID=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$SOURCE_APP/Contents/Info.plist")
if [ "$BUNDLE_ID" != "$EXPECTED_BUNDLE_ID" ]; then
  echo "Refusing to install unexpected bundle identifier: $BUNDLE_ID" >&2
  exit 1
fi
URL_NAME=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleURLTypes:0:CFBundleURLName' "$SOURCE_APP/Contents/Info.plist")
if [ "$URL_NAME" != "$EXPECTED_URL_NAME" ]; then
  echo "Refusing to install unexpected OAuth URL name: $URL_NAME" >&2
  exit 1
fi
URL_SCHEME=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleURLTypes:0:CFBundleURLSchemes:0' "$SOURCE_APP/Contents/Info.plist")
if [ "$URL_SCHEME" != "$EXPECTED_URL_SCHEME" ]; then
  echo "Refusing to install unexpected OAuth URL scheme: $URL_SCHEME" >&2
  exit 1
fi
HELPER_ID=$(codesign -d --verbose=4 "$SOURCE_HELPER" 2>&1 | sed -n 's/^Identifier=//p')
if [ "$HELPER_ID" != "$EXPECTED_HELPER_ID" ]; then
  echo "Refusing to install unexpected helper signing identifier: $HELPER_ID" >&2
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

if [ "$CHECK_ONLY" = "1" ]; then
  echo "Ready to replace $TARGET_APP with the signed local build (team $TEAM_ID)."
  echo "The installed app was not stopped, replaced, or launched."
  exit 0
fi

if pgrep -x "OpenCDX Router" >/dev/null 2>&1; then
  osascript -e 'tell application id "com.dodelidoo.opencdx" to quit' >/dev/null 2>&1 || true
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
