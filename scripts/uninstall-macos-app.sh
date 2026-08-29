#!/bin/sh
set -eu

APP="$HOME/Applications/OpenCDX Router.app"
SUPPORT="$HOME/Library/Application Support/OpenCDX Router"
TRASH="$HOME/.Trash"

if [ -x "$APP/Contents/Resources/router-helper" ]; then
  "$APP/Contents/Resources/router-helper" quit >/dev/null 2>&1 || true
fi
/usr/bin/security delete-generic-password -a device-token -s com.opencdx.router-helper >/dev/null 2>&1 || true
/usr/bin/security delete-generic-password -a enrollment-secret -s com.opencdx.router-helper >/dev/null 2>&1 || true
/usr/bin/security delete-generic-password -a local-token-secret -s com.opencdx.router-helper >/dev/null 2>&1 || true
if [ -d "$APP" ]; then mv "$APP" "$TRASH/OpenCDX Router.app.$(date +%s)"; fi
if [ -d "$SUPPORT" ]; then mv "$SUPPORT" "$TRASH/OpenCDX Router Support.$(date +%s)"; fi
echo "OpenCDX Router was removed. Codex and ~/.codex were not modified."
