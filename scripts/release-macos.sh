#!/usr/bin/env bash
set -euo pipefail

echo "internal-only: macOS archives are for developer QA/build validation and must not be published on GitHub for end users." >&2

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEAM_ID="${CLAMBHOOK_DEVELOPMENT_TEAM:-}"
NOTARY_PROFILE="${NOTARYTOOL_PROFILE:-}"
DIST_DIR="$ROOT_DIR/dist/macos"
ARCHIVE_PATH="$DIST_DIR/ClambhookMac.xcarchive"
EXPORT_PATH="$DIST_DIR/export"
EXPORT_OPTIONS="$DIST_DIR/ExportOptions.DeveloperID.plist"
APP_PATH="$EXPORT_PATH/ClambhookMac.app"
NOTARY_ZIP="$DIST_DIR/ClambhookMac-notary.zip"
FINAL_ZIP="$DIST_DIR/ClambhookMac-arm64.zip"
DAEMON="$ROOT_DIR/bin/clambhook"
SODIUM="$ROOT_DIR/bin/libsodium.26.dylib"
HELPER_ENTITLEMENTS="$ROOT_DIR/ui/apple/ClambhookMac/ClambhookDaemon.entitlements"

if [[ -z "$TEAM_ID" ]]; then
    echo "CLAMBHOOK_DEVELOPMENT_TEAM must be set to your paid Apple Developer Team ID." >&2
    exit 1
fi

if [[ -z "$NOTARY_PROFILE" ]]; then
    echo "NOTARYTOOL_PROFILE must be set to an xcrun notarytool keychain profile." >&2
    exit 1
fi

IDENTITY="$(security find-identity -v -p codesigning | awk -v team="($TEAM_ID)" -F '"' '$2 ~ /^Developer ID Application:/ && index($2, team) > 0 { print $2; exit }')"
if [[ -z "$IDENTITY" ]]; then
    echo "No Developer ID Application signing identity found for team $TEAM_ID." >&2
    security find-identity -v -p codesigning >&2
    exit 1
fi

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

make -C "$ROOT_DIR" build-daemon
"$ROOT_DIR/scripts/prepare-macos-runtime.sh"

codesign --force --timestamp --options runtime --sign "$IDENTITY" "$SODIUM"
codesign --force --timestamp --options runtime --entitlements "$HELPER_ENTITLEMENTS" --sign "$IDENTITY" "$DAEMON"
codesign --verify --strict --verbose=4 "$SODIUM"
codesign --verify --strict --verbose=4 "$DAEMON"

cd "$ROOT_DIR/ui/apple"
xcodegen generate --spec project.yml
cd "$ROOT_DIR"

cat > "$EXPORT_OPTIONS" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>method</key>
    <string>developer-id</string>
    <key>signingStyle</key>
    <string>automatic</string>
    <key>signingCertificate</key>
    <string>Developer ID Application</string>
    <key>teamID</key>
    <string>$TEAM_ID</string>
    <key>stripSwiftSymbols</key>
    <true/>
</dict>
</plist>
PLIST

xcodebuild archive \
    -project "$ROOT_DIR/ui/apple/Clambhook.xcodeproj" \
    -scheme ClambhookMac \
    -configuration Release \
    -destination 'generic/platform=macOS' \
    -archivePath "$ARCHIVE_PATH" \
    DEVELOPMENT_TEAM="$TEAM_ID" \
    CODE_SIGN_STYLE=Automatic \
    CODE_SIGN_IDENTITY="$IDENTITY" \
    OTHER_CODE_SIGN_FLAGS="--timestamp"

xcodebuild -exportArchive \
    -archivePath "$ARCHIVE_PATH" \
    -exportOptionsPlist "$EXPORT_OPTIONS" \
    -exportPath "$EXPORT_PATH" \
    DEVELOPMENT_TEAM="$TEAM_ID"

if [[ ! -d "$APP_PATH" ]]; then
    echo "expected exported app at $APP_PATH" >&2
    exit 1
fi

if otool -L "$APP_PATH/Contents/MacOS/clambhook" | grep -q '/opt/homebrew'; then
    echo "exported daemon still contains a Homebrew runtime dependency" >&2
    otool -L "$APP_PATH/Contents/MacOS/clambhook" >&2
    exit 1
fi

codesign --verify --deep --strict --verbose=4 "$APP_PATH"
ditto -c -k --keepParent "$APP_PATH" "$NOTARY_ZIP"
xcrun notarytool submit "$NOTARY_ZIP" --keychain-profile "$NOTARY_PROFILE" --wait
xcrun stapler staple "$APP_PATH"
xcrun stapler validate "$APP_PATH"
spctl -a -vvv -t exec "$APP_PATH"
ditto -c -k --keepParent "$APP_PATH" "$FINAL_ZIP"

echo "Created $FINAL_ZIP"
