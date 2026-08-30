#!/bin/sh
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
APP_DIR="$REPO_ROOT/dist/OpenCDX Router.app"
EXISTING_HELPER="$APP_DIR/Contents/Resources/router-helper"
SIGNING_IDENTITY_FILE="$REPO_ROOT/.opencdx-codesign-identity"
STAGING_DIR=$(mktemp -d "${TMPDIR:-/tmp}/opencdx-app.XXXXXX")
trap 'rm -rf "$STAGING_DIR"' EXIT INT TERM

APP_VERSION=${OPENCODEX_VERSION:-$(tr -d '[:space:]' < "$REPO_ROOT/VERSION")}
if ! printf '%s\n' "$APP_VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "VERSION must contain a semantic X.Y.Z version (found: $APP_VERSION)." >&2
  exit 1
fi
if [ -n "${OPENCODEX_COMMIT:-}" ]; then
  APP_COMMIT=$OPENCODEX_COMMIT
else
  APP_COMMIT=$(git -C "$REPO_ROOT" rev-parse --short=12 HEAD 2>/dev/null || printf unknown)
fi
if [ -n "${OPENCODEX_BUILD_NUMBER:-}" ]; then
  BUILD_NUMBER=$OPENCODEX_BUILD_NUMBER
else
  BUILD_NUMBER=$(git -C "$REPO_ROOT" rev-list --count HEAD 2>/dev/null || printf 1)
fi
case "$BUILD_NUMBER" in
  ''|*[!0-9]*) echo "OPENCODEX_BUILD_NUMBER must be numeric." >&2; exit 1 ;;
esac

RELEASE_BUILD=${OPENCODEX_RELEASE_BUILD:-0}
UNIVERSAL_BUILD=${OPENCODEX_UNIVERSAL:-$RELEASE_BUILD}
SIGNING_IDENTITY=${OPENCODEX_CODESIGN_IDENTITY:-}
if [ -z "$SIGNING_IDENTITY" ] && [ -f "$SIGNING_IDENTITY_FILE" ]; then
  IFS= read -r SIGNING_IDENTITY < "$SIGNING_IDENTITY_FILE" || true
fi
if [ -z "$SIGNING_IDENTITY" ]; then
  SIGNING_IDENTITY=-
fi
if [ "$RELEASE_BUILD" = "1" ] && [ "$SIGNING_IDENTITY" = "-" ]; then
  echo "A Developer ID Application identity is required for a release build." >&2
  exit 1
fi

STAGED_APP="$STAGING_DIR/OpenCDX Router.app"
STAGED_HELPER="$STAGED_APP/Contents/Resources/router-helper"
STAGED_MENU="$STAGED_APP/Contents/MacOS/OpenCDX Router"
mkdir -p "$REPO_ROOT/dist" "$STAGED_APP/Contents/MacOS" "$STAGED_APP/Contents/Resources"

build_helper() {
  if [ -n "${HELPER_BINARY:-}" ]; then
    cp "$HELPER_BINARY" "$STAGED_HELPER"
    return
  fi
  if ! command -v go >/dev/null 2>&1; then
    if [ "$UNIVERSAL_BUILD" != "1" ] && [ -x "$EXISTING_HELPER" ]; then
      echo "Go was not found; reusing the existing bundled router-helper."
      cp "$EXISTING_HELPER" "$STAGED_HELPER"
      return
    fi
    echo "Go is required, or set HELPER_BINARY to a prebuilt macOS router-helper." >&2
    exit 1
  fi
  LDFLAGS="-s -w -X github.com/Dodelidoo-Labs/open-cdx/internal/version.Version=$APP_VERSION -X github.com/Dodelidoo-Labs/open-cdx/internal/version.Commit=$APP_COMMIT"
  if [ "$UNIVERSAL_BUILD" = "1" ]; then
    for GO_ARCH in arm64 amd64; do
      (cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH="$GO_ARCH" go build -trimpath -ldflags="$LDFLAGS" -o "$STAGING_DIR/router-helper-$GO_ARCH" ./cmd/router-helper)
    done
    lipo -create "$STAGING_DIR/router-helper-arm64" "$STAGING_DIR/router-helper-amd64" -output "$STAGED_HELPER"
  else
    (cd "$REPO_ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS" -o "$STAGED_HELPER" ./cmd/router-helper)
  fi
}

build_menu_app() {
  SWIFT_ROOT="$REPO_ROOT/mac/RouterMenu"
  if [ "$UNIVERSAL_BUILD" = "1" ]; then
    for SWIFT_ARCH in arm64 x86_64; do
      SWIFT_SCRATCH="$SWIFT_ROOT/.build-$SWIFT_ARCH"
      SWIFT_MODULE_CACHE="$SWIFT_SCRATCH/ModuleCache"
      mkdir -p "$SWIFT_MODULE_CACHE"
      (cd "$SWIFT_ROOT" && CLANG_MODULE_CACHE_PATH="$SWIFT_MODULE_CACHE" SWIFTPM_MODULECACHE_OVERRIDE="$SWIFT_MODULE_CACHE" swift build --disable-sandbox -c release --scratch-path "$SWIFT_SCRATCH" --triple "$SWIFT_ARCH-apple-macosx13.0")
      SWIFT_BIN_DIR=$(cd "$SWIFT_ROOT" && CLANG_MODULE_CACHE_PATH="$SWIFT_MODULE_CACHE" SWIFTPM_MODULECACHE_OVERRIDE="$SWIFT_MODULE_CACHE" swift build --disable-sandbox -c release --scratch-path "$SWIFT_SCRATCH" --triple "$SWIFT_ARCH-apple-macosx13.0" --show-bin-path)
      cp "$SWIFT_BIN_DIR/OpenCDXRouterMenu" "$STAGING_DIR/OpenCDXRouterMenu-$SWIFT_ARCH"
    done
    lipo -create "$STAGING_DIR/OpenCDXRouterMenu-arm64" "$STAGING_DIR/OpenCDXRouterMenu-x86_64" -output "$STAGED_MENU"
  else
    SWIFT_SCRATCH="$SWIFT_ROOT/.build"
    SWIFT_MODULE_CACHE="$SWIFT_SCRATCH/ModuleCache"
    mkdir -p "$SWIFT_MODULE_CACHE"
    (cd "$SWIFT_ROOT" && CLANG_MODULE_CACHE_PATH="$SWIFT_MODULE_CACHE" SWIFTPM_MODULECACHE_OVERRIDE="$SWIFT_MODULE_CACHE" swift build --disable-sandbox -c release --scratch-path "$SWIFT_SCRATCH")
    SWIFT_BIN_DIR=$(cd "$SWIFT_ROOT" && CLANG_MODULE_CACHE_PATH="$SWIFT_MODULE_CACHE" SWIFTPM_MODULECACHE_OVERRIDE="$SWIFT_MODULE_CACHE" swift build --disable-sandbox -c release --scratch-path "$SWIFT_SCRATCH" --show-bin-path)
    cp "$SWIFT_BIN_DIR/OpenCDXRouterMenu" "$STAGED_MENU"
  fi
}

build_helper
build_menu_app
cp "$REPO_ROOT/mac/RouterMenu/Info.plist" "$STAGED_APP/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $APP_VERSION" "$STAGED_APP/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $BUILD_NUMBER" "$STAGED_APP/Contents/Info.plist"
chmod 755 "$STAGED_MENU" "$STAGED_HELPER"

if [ "$SIGNING_IDENTITY" = "-" ]; then
  echo "No development signing identity configured; using an ad-hoc signature." >&2
  codesign --force --sign - "$STAGED_HELPER"
  codesign --force --sign - "$STAGED_APP"
else
  if ! security find-identity -v -p codesigning | grep -F "$SIGNING_IDENTITY" >/dev/null; then
    echo "Configured macOS signing identity is unavailable or invalid: $SIGNING_IDENTITY" >&2
    exit 1
  fi
  if [ "$RELEASE_BUILD" = "1" ]; then
    codesign --force --options runtime --timestamp --sign "$SIGNING_IDENTITY" "$STAGED_HELPER"
    codesign --force --options runtime --timestamp --sign "$SIGNING_IDENTITY" "$STAGED_APP"
    echo "Signed hardened release with: $SIGNING_IDENTITY"
  else
    codesign --force --timestamp=none --sign "$SIGNING_IDENTITY" "$STAGED_HELPER"
    codesign --force --timestamp=none --sign "$SIGNING_IDENTITY" "$STAGED_APP"
    echo "Signed development build with: $SIGNING_IDENTITY"
  fi
fi
codesign --verify --deep --strict --verbose=2 "$STAGED_APP"
if [ -e "$APP_DIR" ]; then
  mv "$APP_DIR" "$STAGING_DIR/previous.app"
fi
mv "$STAGED_APP" "$APP_DIR"
echo "$APP_DIR"
