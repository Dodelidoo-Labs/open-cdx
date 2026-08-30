#!/bin/sh
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SOURCE_ICON="$REPO_ROOT/assets/branding/opencdx-logo.png"
APP_ICON_CONTENTS="$REPO_ROOT/assets/branding/macos-app-icon-contents.json"

if [ ! -f "$SOURCE_ICON" ]; then
  echo "OpenCDX icon master is missing: $SOURCE_ICON" >&2
  exit 1
fi
if ! command -v sips >/dev/null 2>&1; then
  echo "sips is required to generate OpenCDX icon assets." >&2
  exit 1
fi

resize_png() {
  resize_size=$1
  resize_destination=$2
  sips -z "$resize_size" "$resize_size" "$SOURCE_ICON" --out "$resize_destination" >/dev/null
}

generate_web_assets() {
  WEB_DIR="$REPO_ROOT/web/static"
  mkdir -p "$WEB_DIR"
  resize_png 256 "$WEB_DIR/opencdx-router-logo.png"
  resize_png 32 "$WEB_DIR/favicon-32x32.png"
  resize_png 16 "$WEB_DIR/favicon-16x16.png"
  resize_png 180 "$WEB_DIR/apple-touch-icon.png"
  echo "Generated OpenCDX web icon assets in $WEB_DIR"
}

generate_icns() {
  icns_destination=$1
  if ! command -v xcrun >/dev/null 2>&1; then
    echo "Xcode command-line tools are required to generate the macOS application icon." >&2
    exit 1
  fi

  TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/opencdx-icons.XXXXXX")
  trap 'rm -rf "$TEMP_DIR"' EXIT INT TERM
  ASSET_CATALOG="$TEMP_DIR/OpenCDXAssets.xcassets"
  APP_ICON_SET="$ASSET_CATALOG/AppIcon.appiconset"
  COMPILED_ASSETS="$TEMP_DIR/compiled"
  mkdir -p "$APP_ICON_SET" "$COMPILED_ASSETS" "$(dirname -- "$icns_destination")"
  cp "$APP_ICON_CONTENTS" "$APP_ICON_SET/Contents.json"

  resize_png 16 "$APP_ICON_SET/icon_16x16.png"
  resize_png 32 "$APP_ICON_SET/icon_16x16@2x.png"
  resize_png 32 "$APP_ICON_SET/icon_32x32.png"
  resize_png 64 "$APP_ICON_SET/icon_32x32@2x.png"
  resize_png 128 "$APP_ICON_SET/icon_128x128.png"
  resize_png 256 "$APP_ICON_SET/icon_128x128@2x.png"
  resize_png 256 "$APP_ICON_SET/icon_256x256.png"
  resize_png 512 "$APP_ICON_SET/icon_256x256@2x.png"
  resize_png 512 "$APP_ICON_SET/icon_512x512.png"
  resize_png 1024 "$APP_ICON_SET/icon_512x512@2x.png"

  xcrun actool "$ASSET_CATALOG" \
    --compile "$COMPILED_ASSETS" \
    --platform macosx \
    --minimum-deployment-target 13.0 \
    --app-icon AppIcon \
    --output-partial-info-plist "$TEMP_DIR/AppIcon.plist" \
    --warnings --errors --notices >/dev/null
  cp "$COMPILED_ASSETS/AppIcon.icns" "$icns_destination"
  cp "$COMPILED_ASSETS/Assets.car" "$(dirname -- "$icns_destination")/Assets.car"
}

case "${1:-web}" in
  web)
    generate_web_assets
    ;;
  icns)
    if [ "$#" -ne 2 ]; then
      echo "Usage: $0 icns OUTPUT_PATH" >&2
      exit 1
    fi
    generate_icns "$2"
    ;;
  *)
    echo "Usage: $0 [web | icns OUTPUT_PATH]" >&2
    exit 1
    ;;
esac
