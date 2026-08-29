#!/bin/sh
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
APP_DIR="$REPO_ROOT/dist/OpenCDX Router.app"
EXISTING_HELPER="$APP_DIR/Contents/Resources/router-helper"
SIGNING_IDENTITY_FILE="$REPO_ROOT/.opencdx-codesign-identity"
STAGING_DIR=$(mktemp -d "${TMPDIR:-/tmp}/opencdx-app.XXXXXX")
trap 'rm -rf "$STAGING_DIR"' EXIT INT TERM

SIGNING_IDENTITY=${OPENCODEX_CODESIGN_IDENTITY:-}
if [ -z "$SIGNING_IDENTITY" ] && [ -f "$SIGNING_IDENTITY_FILE" ]; then
  IFS= read -r SIGNING_IDENTITY < "$SIGNING_IDENTITY_FILE" || true
fi
if [ -z "$SIGNING_IDENTITY" ]; then
  SIGNING_IDENTITY=-
fi

mkdir -p "$REPO_ROOT/dist" "$STAGING_DIR/OpenCDX Router.app/Contents/MacOS" "$STAGING_DIR/OpenCDX Router.app/Contents/Resources"

if [ -n "${HELPER_BINARY:-}" ]; then
  cp "$HELPER_BINARY" "$STAGING_DIR/OpenCDX Router.app/Contents/Resources/router-helper"
elif command -v go >/dev/null 2>&1; then
  (cd "$REPO_ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$STAGING_DIR/OpenCDX Router.app/Contents/Resources/router-helper" ./cmd/router-helper)
elif [ -x "$EXISTING_HELPER" ]; then
  echo "Go was not found; reusing the existing bundled router-helper."
  cp "$EXISTING_HELPER" "$STAGING_DIR/OpenCDX Router.app/Contents/Resources/router-helper"
else
  echo "Go is required for the first build, or set HELPER_BINARY to a prebuilt macOS router-helper." >&2
  exit 1
fi

SWIFT_SCRATCH="$REPO_ROOT/mac/RouterMenu/.build"
SWIFT_MODULE_CACHE="$SWIFT_SCRATCH/ModuleCache"
mkdir -p "$SWIFT_MODULE_CACHE"
(cd "$REPO_ROOT/mac/RouterMenu" && CLANG_MODULE_CACHE_PATH="$SWIFT_MODULE_CACHE" SWIFTPM_MODULECACHE_OVERRIDE="$SWIFT_MODULE_CACHE" swift build --disable-sandbox -c release --scratch-path "$SWIFT_SCRATCH")
SWIFT_BINARY=$(cd "$REPO_ROOT/mac/RouterMenu" && CLANG_MODULE_CACHE_PATH="$SWIFT_MODULE_CACHE" SWIFTPM_MODULECACHE_OVERRIDE="$SWIFT_MODULE_CACHE" swift build --disable-sandbox -c release --scratch-path "$SWIFT_SCRATCH" --show-bin-path)/OpenCDXRouterMenu
cp "$SWIFT_BINARY" "$STAGING_DIR/OpenCDX Router.app/Contents/MacOS/OpenCDX Router"
cp "$REPO_ROOT/mac/RouterMenu/Info.plist" "$STAGING_DIR/OpenCDX Router.app/Contents/Info.plist"
chmod 755 "$STAGING_DIR/OpenCDX Router.app/Contents/MacOS/OpenCDX Router" "$STAGING_DIR/OpenCDX Router.app/Contents/Resources/router-helper"

if [ "$SIGNING_IDENTITY" = "-" ]; then
  echo "No development signing identity configured; using an ad-hoc signature." >&2
  codesign --force --sign - "$STAGING_DIR/OpenCDX Router.app/Contents/Resources/router-helper"
  codesign --force --sign - "$STAGING_DIR/OpenCDX Router.app"
else
  if ! security find-identity -v -p codesigning | grep -F "$SIGNING_IDENTITY" >/dev/null; then
    echo "Configured macOS signing identity is unavailable or invalid: $SIGNING_IDENTITY" >&2
    exit 1
  fi
  codesign --force --timestamp=none --sign "$SIGNING_IDENTITY" "$STAGING_DIR/OpenCDX Router.app/Contents/Resources/router-helper"
  codesign --force --timestamp=none --sign "$SIGNING_IDENTITY" "$STAGING_DIR/OpenCDX Router.app"
  echo "Signed with configured Apple development identity: $SIGNING_IDENTITY"
fi
codesign --verify --deep --strict "$STAGING_DIR/OpenCDX Router.app"
if [ -e "$APP_DIR" ]; then
  mv "$APP_DIR" "$STAGING_DIR/previous.app"
fi
mv "$STAGING_DIR/OpenCDX Router.app" "$APP_DIR"
echo "$APP_DIR"
