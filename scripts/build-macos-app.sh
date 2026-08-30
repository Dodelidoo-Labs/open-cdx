#!/bin/sh
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
APP_DIR="$REPO_ROOT/dist/OpenCDX Router.app"
SIGNING_IDENTITY_FILE="$REPO_ROOT/.opencdx-codesign-identity"
GO_BINARY_FILE="$REPO_ROOT/.opencdx-go-binary"
HELPER_SIGNING_IDENTIFIER="com.opencdx.router-menu.helper"
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

  set -- $(security find-identity -v -p codesigning 2>/dev/null | awk '/^[[:space:]]*[0-9]+\)/ { print $2 }')
  case "$#" in
    1)
      SIGNING_IDENTITY=$1
      echo "Using the only available Apple code-signing identity: $SIGNING_IDENTITY"
      ;;
    0)
      echo "No Apple code-signing identity is available." >&2
      echo "Install an Apple Development certificate, configure .opencdx-codesign-identity, or explicitly set OPENCODEX_CODESIGN_IDENTITY=- for a non-installed CI artifact." >&2
      exit 1
      ;;
    *)
      echo "Multiple Apple code-signing identities are available. Put the intended SHA-1 fingerprint in .opencdx-codesign-identity." >&2
      exit 1
      ;;
  esac
fi
if [ "$RELEASE_BUILD" = "1" ] && [ "$SIGNING_IDENTITY" = "-" ]; then
  echo "A Developer ID Application identity is required for a release build." >&2
  exit 1
fi

STAGED_APP="$STAGING_DIR/OpenCDX Router.app"
STAGED_HELPER="$STAGED_APP/Contents/Resources/router-helper"
STAGED_MENU="$STAGED_APP/Contents/MacOS/OpenCDX Router"
STAGED_ICON="$STAGED_APP/Contents/Resources/OpenCDXRouter.icns"
STAGED_MENU_ICON="$STAGED_APP/Contents/Resources/OpenCDXMenuBarTemplate.png"
mkdir -p "$REPO_ROOT/dist" "$STAGED_APP/Contents/MacOS" "$STAGED_APP/Contents/Resources"

build_helper() {
  if [ -n "${HELPER_BINARY:-}" ]; then
    cp "$HELPER_BINARY" "$STAGED_HELPER"
    return
  fi
  GO_BINARY=${OPENCODEX_GO_BINARY:-}
  if [ -z "$GO_BINARY" ] && [ -f "$GO_BINARY_FILE" ]; then
    IFS= read -r GO_BINARY < "$GO_BINARY_FILE" || true
  fi
  if [ -z "$GO_BINARY" ]; then
    GO_BINARY=$(command -v go 2>/dev/null || true)
  fi
  if [ -z "$GO_BINARY" ] || [ ! -x "$GO_BINARY" ]; then
    echo "Go is required to build the bundled helper; stale helpers are never reused." >&2
    echo "Install Go, set OPENCODEX_GO_BINARY, configure .opencdx-go-binary, or set HELPER_BINARY to a current prebuilt helper." >&2
    exit 1
  fi
  LDFLAGS="-s -w -X github.com/Dodelidoo-Labs/open-cdx/internal/version.Version=$APP_VERSION -X github.com/Dodelidoo-Labs/open-cdx/internal/version.Commit=$APP_COMMIT"
  if [ "$UNIVERSAL_BUILD" = "1" ]; then
    for GO_ARCH in arm64 amd64; do
      (cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH="$GO_ARCH" "$GO_BINARY" build -trimpath -ldflags="$LDFLAGS" -o "$STAGING_DIR/router-helper-$GO_ARCH" ./cmd/router-helper)
    done
    lipo -create "$STAGING_DIR/router-helper-arm64" "$STAGING_DIR/router-helper-amd64" -output "$STAGED_HELPER"
  else
    (cd "$REPO_ROOT" && CGO_ENABLED=0 "$GO_BINARY" build -trimpath -ldflags="$LDFLAGS" -o "$STAGED_HELPER" ./cmd/router-helper)
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
"$REPO_ROOT/scripts/generate-icon-assets.sh" icns "$STAGED_ICON"
"$REPO_ROOT/scripts/generate-icon-assets.sh" menubar "$STAGED_MENU_ICON"
cp "$REPO_ROOT/mac/RouterMenu/Info.plist" "$STAGED_APP/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $APP_VERSION" "$STAGED_APP/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $BUILD_NUMBER" "$STAGED_APP/Contents/Info.plist"
chmod 755 "$STAGED_MENU" "$STAGED_HELPER"

if [ "$SIGNING_IDENTITY" = "-" ]; then
  echo "Using an explicitly requested ad-hoc signature; do not install this build because macOS cannot preserve its privacy identity across rebuilds." >&2
  codesign --force --identifier "$HELPER_SIGNING_IDENTIFIER" --sign - "$STAGED_HELPER"
  codesign --force --sign - "$STAGED_APP"
else
  if ! security find-identity -v -p codesigning | grep -F "$SIGNING_IDENTITY" >/dev/null; then
    echo "Configured macOS signing identity is unavailable or invalid: $SIGNING_IDENTITY" >&2
    exit 1
  fi
  if [ "$RELEASE_BUILD" = "1" ]; then
    codesign --force --identifier "$HELPER_SIGNING_IDENTIFIER" --options runtime --timestamp --sign "$SIGNING_IDENTITY" "$STAGED_HELPER"
    codesign --force --options runtime --timestamp --sign "$SIGNING_IDENTITY" "$STAGED_APP"
    echo "Signed hardened release with: $SIGNING_IDENTITY"
  else
    codesign --force --identifier "$HELPER_SIGNING_IDENTIFIER" --timestamp=none --sign "$SIGNING_IDENTITY" "$STAGED_HELPER"
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
