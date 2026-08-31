#!/bin/sh
set -eu

APP="$HOME/Applications/OpenCDX Router.app"
SUPPORT="$HOME/Library/Application Support/com.dodelidoo.opencdx"
TRASH="$HOME/.Trash"
KEYCHAIN_SERVICE="com.dodelidoo.opencdx.helper"
PREFERENCES_DOMAIN="com.dodelidoo.opencdx"

if [ -x "$APP/Contents/Resources/router-helper" ]; then
  "$APP/Contents/Resources/router-helper" quit >/dev/null 2>&1 || true
fi
/usr/bin/security delete-generic-password -a device-token -s "$KEYCHAIN_SERVICE" >/dev/null 2>&1 || true
/usr/bin/security delete-generic-password -a enrollment-secret -s "$KEYCHAIN_SERVICE" >/dev/null 2>&1 || true
/usr/bin/security delete-generic-password -a local-token-secret -s "$KEYCHAIN_SERVICE" >/dev/null 2>&1 || true
/usr/bin/defaults delete "$PREFERENCES_DOMAIN" >/dev/null 2>&1 || true
if [ -d "$APP" ]; then mv "$APP" "$TRASH/OpenCDX Router.app.$(date +%s)"; fi
if [ -d "$SUPPORT" ]; then mv "$SUPPORT" "$TRASH/OpenCDX Router Support.$(date +%s)"; fi
echo "OpenCDX Router was removed. Codex and ~/.codex were not modified."
