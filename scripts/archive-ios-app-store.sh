#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEAM_ID="${CLAMBHOOK_DEVELOPMENT_TEAM:-}"
API_KEY_PATH="${CLAMBHOOK_APP_STORE_CONNECT_API_KEY_PATH:-}"
API_KEY_ID="${CLAMBHOOK_APP_STORE_CONNECT_API_KEY_ID:-}"
API_ISSUER_ID="${CLAMBHOOK_APP_STORE_CONNECT_API_ISSUER_ID:-}"
DIST_DIR="$ROOT_DIR/dist/ios"
ARCHIVE_PATH="$DIST_DIR/ClambhookiOS.xcarchive"
EXPORT_PATH="$DIST_DIR/export"
EXPORT_OPTIONS="$DIST_DIR/ExportOptions.AppStore.plist"
PROJECT_PATH="$ROOT_DIR/ui/apple/Clambhook.xcodeproj"
SCHEME="ClambhookiOS"

require_command() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "$1 is required to archive the iPhone app." >&2
        echo "$2" >&2
        exit 127
    fi
}

if [[ -z "$TEAM_ID" ]]; then
    echo "CLAMBHOOK_DEVELOPMENT_TEAM must be set to your Apple Developer Team ID." >&2
    exit 1
fi

api_key_values=0
for value in "$API_KEY_PATH" "$API_KEY_ID" "$API_ISSUER_ID"; do
    if [[ -n "$value" ]]; then
        api_key_values=$((api_key_values + 1))
    fi
done

if [[ "$api_key_values" -ne 0 && "$api_key_values" -ne 3 ]]; then
    echo "Set all App Store Connect API key variables, or leave all of them unset:" >&2
    echo "  CLAMBHOOK_APP_STORE_CONNECT_API_KEY_PATH" >&2
    echo "  CLAMBHOOK_APP_STORE_CONNECT_API_KEY_ID" >&2
    echo "  CLAMBHOOK_APP_STORE_CONNECT_API_ISSUER_ID" >&2
    exit 1
fi

provisioning_args=(-allowProvisioningUpdates)
if [[ "$api_key_values" -eq 3 ]]; then
    if [[ ! -f "$API_KEY_PATH" ]]; then
        echo "CLAMBHOOK_APP_STORE_CONNECT_API_KEY_PATH does not exist: $API_KEY_PATH" >&2
        exit 1
    fi
    provisioning_args+=(
        -authenticationKeyPath "$API_KEY_PATH"
        -authenticationKeyID "$API_KEY_ID"
        -authenticationKeyIssuerID "$API_ISSUER_ID"
    )
fi

require_command xcodebuild "Install Xcode and select it with xcode-select."
require_command xcodegen "Install XcodeGen, for example: brew install xcodegen"

rm -rf "$ARCHIVE_PATH" "$EXPORT_PATH" "$EXPORT_OPTIONS"
mkdir -p "$DIST_DIR"

"$ROOT_DIR/scripts/build-ios-mobile-xcframework.sh"

cd "$ROOT_DIR/ui/apple"
xcodegen generate --spec project.yml
cd "$ROOT_DIR"

cat > "$EXPORT_OPTIONS" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>method</key>
    <string>app-store-connect</string>
    <key>destination</key>
    <string>export</string>
    <key>signingStyle</key>
    <string>automatic</string>
    <key>teamID</key>
    <string>$TEAM_ID</string>
    <key>stripSwiftSymbols</key>
    <true/>
</dict>
</plist>
PLIST

xcodebuild archive \
    -project "$PROJECT_PATH" \
    -scheme "$SCHEME" \
    -configuration Release \
    -destination 'generic/platform=iOS' \
    -archivePath "$ARCHIVE_PATH" \
    DEVELOPMENT_TEAM="$TEAM_ID" \
    CODE_SIGN_STYLE=Automatic \
    "${provisioning_args[@]}"

xcodebuild -exportArchive \
    -archivePath "$ARCHIVE_PATH" \
    -exportOptionsPlist "$EXPORT_OPTIONS" \
    -exportPath "$EXPORT_PATH" \
    DEVELOPMENT_TEAM="$TEAM_ID" \
    "${provisioning_args[@]}"

ipa_count=0
ipa_path=""
while IFS= read -r path; do
    ipa_count=$((ipa_count + 1))
    ipa_path="$path"
done < <(find "$EXPORT_PATH" -maxdepth 1 -type f -name '*.ipa' -print)

if [[ "$ipa_count" -ne 1 ]]; then
    echo "expected exactly one exported IPA in $EXPORT_PATH, found $ipa_count" >&2
    find "$EXPORT_PATH" -maxdepth 1 -type f -print >&2
    exit 1
fi

echo "Created $ipa_path"
