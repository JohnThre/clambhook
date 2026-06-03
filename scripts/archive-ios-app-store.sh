#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEAM_ID="${CLAMBHOOK_DEVELOPMENT_TEAM:-}"
API_KEY_PATH="${CLAMBHOOK_APP_STORE_CONNECT_API_KEY_PATH:-}"
API_KEY_ID="${CLAMBHOOK_APP_STORE_CONNECT_API_KEY_ID:-}"
API_ISSUER_ID="${CLAMBHOOK_APP_STORE_CONNECT_API_ISSUER_ID:-}"
EXPECTED_TEAM_ID="V6GG4HYABJ"
APP_BUNDLE_ID="org.jpfchang.clambhook"
TUNNEL_BUNDLE_ID="org.jpfchang.clambhook.tunnel"
WIDGET_BUNDLE_ID="org.jpfchang.clambhook.widgets"
APP_GROUP_ID="group.org.jpfchang.clambhook"
NETWORK_EXTENSION_VALUE="packet-tunnel-provider"
SOURCE_APP_ENTITLEMENTS="$ROOT_DIR/ui/apple/ClambhookiOS/ClambhookiOS.entitlements"
SOURCE_TUNNEL_ENTITLEMENTS="$ROOT_DIR/ui/apple/ClambhookPacketTunnel/ClambhookPacketTunnel.entitlements"
SOURCE_WIDGET_ENTITLEMENTS="$ROOT_DIR/ui/apple/ClambhookWidgets/ClambhookiOSWidgetExtension.entitlements"
DIST_DIR="$ROOT_DIR/dist/ios"
ARCHIVE_PATH="$DIST_DIR/ClambhookiOS.xcarchive"
EXPORT_PATH="$DIST_DIR/export"
EXPORT_OPTIONS="$DIST_DIR/ExportOptions.AppStore.plist"
SIGNING_PROOF="$DIST_DIR/signing-proof.txt"
PROJECT_PATH="$ROOT_DIR/ui/apple/Clambhook.xcodeproj"
SCHEME="ClambhookiOS"
PLIST_BUDDY="/usr/libexec/PlistBuddy"

fail() {
    echo "$1" >&2
    exit 1
}

require_command() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "$1 is required to archive the iPhone app." >&2
        echo "$2" >&2
        exit 127
    fi
}

require_file() {
    if [[ ! -f "$1" ]]; then
        fail "$2 does not exist: $1"
    fi
}

append_proof() {
    printf '%s\n' "$*" >> "$SIGNING_PROOF"
}

if [[ -z "$TEAM_ID" ]]; then
    fail "CLAMBHOOK_DEVELOPMENT_TEAM must be set to $EXPECTED_TEAM_ID."
fi

if [[ "$TEAM_ID" != "$EXPECTED_TEAM_ID" ]]; then
    fail "CLAMBHOOK_DEVELOPMENT_TEAM must be $EXPECTED_TEAM_ID for the final App Store release, got $TEAM_ID."
fi

if [[ -z "$API_KEY_PATH" || -z "$API_KEY_ID" || -z "$API_ISSUER_ID" ]]; then
    fail "Set all App Store Connect API key variables for automatic App Store provisioning:
  CLAMBHOOK_APP_STORE_CONNECT_API_KEY_PATH
  CLAMBHOOK_APP_STORE_CONNECT_API_KEY_ID
  CLAMBHOOK_APP_STORE_CONNECT_API_ISSUER_ID"
fi

require_file "$API_KEY_PATH" "CLAMBHOOK_APP_STORE_CONNECT_API_KEY_PATH"

provisioning_args=(-allowProvisioningUpdates)
provisioning_args+=(
    -authenticationKeyPath "$API_KEY_PATH"
    -authenticationKeyID "$API_KEY_ID"
    -authenticationKeyIssuerID "$API_ISSUER_ID"
)

require_command xcodebuild "Install Xcode and select it with xcode-select."
require_command xcodegen "Install XcodeGen, for example: brew install xcodegen"
require_command codesign "Install Xcode command line tools."
require_command security "Install Xcode command line tools."
require_command unzip "Install unzip."

if [[ ! -x "$PLIST_BUDDY" ]]; then
    fail "$PLIST_BUDDY is required to verify signing metadata."
fi

KEYCHAIN_GROUP_ID="$TEAM_ID.$APP_BUNDLE_ID"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-ios-signing.XXXXXX")"
cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

plist_value() {
    local plist="$1"
    local key="$2"
    "$PLIST_BUDDY" -c "Print :$key" "$plist" 2>/dev/null
}

plist_expect_value() {
    local plist="$1"
    local key="$2"
    local expected="$3"
    local label="$4"
    local actual

    if ! actual="$(plist_value "$plist" "$key")"; then
        fail "$label is missing $key."
    fi
    if [[ "$actual" != "$expected" ]]; then
        fail "$label has $key=$actual, expected $expected."
    fi
    append_proof "- $label $key: $actual"
}

plist_expect_array_contains() {
    local plist="$1"
    local key="$2"
    local expected="$3"
    local label="$4"
    local values

    if ! values="$(plist_value "$plist" "$key")"; then
        fail "$label is missing $key."
    fi
    if ! printf '%s\n' "$values" | sed 's/^[[:space:]]*//' | grep -Fxq "$expected"; then
        fail "$label $key does not include $expected."
    fi
    append_proof "- $label $key includes: $expected"
}

decode_profile() {
    local profile_path="$1"
    local output_path="$2"

    security cms -D -i "$profile_path" > "$output_path" 2>/dev/null || \
        fail "Could not decode provisioning profile: $profile_path"
}

verify_signed_bundle() {
    local label="$1"
    local bundle_path="$2"
    local bundle_id="$3"
    local entitlements_path="$TMP_DIR/$label.entitlements.plist"
    local profile_path="$TMP_DIR/$label.profile.plist"

    [[ -d "$bundle_path" ]] || fail "$label bundle is missing: $bundle_path"
    require_file "$bundle_path/Info.plist" "$label Info.plist"
    require_file "$bundle_path/embedded.mobileprovision" "$label embedded.mobileprovision"

    append_proof "### $label"
    append_proof "- Bundle path: $bundle_path"

    codesign --verify --strict --verbose=2 "$bundle_path" >/dev/null 2>&1 || \
        fail "$label failed codesign verification."
    append_proof "- Codesign verification: passed"

    codesign -d --entitlements :- "$bundle_path" > "$entitlements_path" 2>/dev/null || \
        fail "Could not read codesign entitlements for $label."
    decode_profile "$bundle_path/embedded.mobileprovision" "$profile_path"

    plist_expect_value "$bundle_path/Info.plist" "CFBundleIdentifier" "$bundle_id" "$label Info.plist"

    plist_expect_value "$entitlements_path" "application-identifier" "$TEAM_ID.$bundle_id" "$label codesign entitlements"
    plist_expect_value "$entitlements_path" "com.apple.developer.team-identifier" "$TEAM_ID" "$label codesign entitlements"
    plist_expect_array_contains "$entitlements_path" "com.apple.security.application-groups" "$APP_GROUP_ID" "$label codesign entitlements"
    plist_expect_array_contains "$entitlements_path" "keychain-access-groups" "$KEYCHAIN_GROUP_ID" "$label codesign entitlements"
    plist_expect_array_contains "$entitlements_path" "com.apple.developer.networking.networkextension" "$NETWORK_EXTENSION_VALUE" "$label codesign entitlements"

    plist_expect_value "$profile_path" "Entitlements:application-identifier" "$TEAM_ID.$bundle_id" "$label provisioning profile"
    plist_expect_value "$profile_path" "Entitlements:com.apple.developer.team-identifier" "$TEAM_ID" "$label provisioning profile"
    plist_expect_array_contains "$profile_path" "Entitlements:com.apple.security.application-groups" "$APP_GROUP_ID" "$label provisioning profile"
    plist_expect_array_contains "$profile_path" "Entitlements:keychain-access-groups" "$KEYCHAIN_GROUP_ID" "$label provisioning profile"
    plist_expect_array_contains "$profile_path" "Entitlements:com.apple.developer.networking.networkextension" "$NETWORK_EXTENSION_VALUE" "$label provisioning profile"
    append_proof ""
}

verify_payload_tree() {
    local label="$1"
    local app_path="$2"

    verify_signed_bundle "$label app" "$app_path" "$APP_BUNDLE_ID"
    verify_signed_bundle "$label packet tunnel" "$app_path/PlugIns/ClambhookPacketTunnel.appex" "$TUNNEL_BUNDLE_ID"
    verify_signed_bundle "$label iOS widget" "$app_path/PlugIns/ClambhookiOSWidgetExtension.appex" "$WIDGET_BUNDLE_ID"
}

verify_source_entitlements() {
    local label="$1"
    local entitlements_path="$2"

    require_file "$entitlements_path" "$label source entitlements"

    append_proof "### $label source entitlements"
    append_proof "- Entitlements path: $entitlements_path"
    plist_expect_array_contains "$entitlements_path" "com.apple.security.application-groups" "$APP_GROUP_ID" "$label source entitlements"
    plist_expect_array_contains "$entitlements_path" "keychain-access-groups" '$(AppIdentifierPrefix)org.jpfchang.clambhook' "$label source entitlements"
    plist_expect_array_contains "$entitlements_path" "com.apple.developer.networking.networkextension" "$NETWORK_EXTENSION_VALUE" "$label source entitlements"
    append_proof ""
}

write_build_settings_proof() {
    local target="$1"

    append_proof "## Build Settings: $target"
    xcodebuild \
        -project "$PROJECT_PATH" \
        -target "$target" \
        -configuration Release \
        -destination 'generic/platform=iOS' \
        DEVELOPMENT_TEAM="$TEAM_ID" \
        CODE_SIGN_STYLE=Automatic \
        -showBuildSettings 2>&1 | awk '
            /Build settings for action build and target / { print; next }
            /^[[:space:]]+(APPLICATION_EXTENSION_API_ONLY|CODE_SIGN_ENTITLEMENTS|CODE_SIGN_STYLE|DEVELOPMENT_TEAM|PRODUCT_BUNDLE_IDENTIFIER|SKIP_INSTALL|TARGETED_DEVICE_FAMILY) = / { print }
        ' >> "$SIGNING_PROOF"
    append_proof ""
}

rm -rf "$ARCHIVE_PATH" "$EXPORT_PATH" "$EXPORT_OPTIONS" "$SIGNING_PROOF"
mkdir -p "$DIST_DIR"

"$ROOT_DIR/scripts/app-review-compliance-check.sh" --require-demo-secret

"$ROOT_DIR/scripts/build-ios-mobile-xcframework.sh"

cd "$ROOT_DIR/ui/apple"
xcodegen generate --spec project.yml
cd "$ROOT_DIR"

cat > "$SIGNING_PROOF" <<PROOF
# Clambhook iPhone App Store Signing Proof

- Generated at: $(date -u +"%Y-%m-%dT%H:%M:%SZ")
- Team ID: $TEAM_ID
- App bundle ID: $APP_BUNDLE_ID
- Packet tunnel bundle ID: $TUNNEL_BUNDLE_ID
- Widget bundle ID: $WIDGET_BUNDLE_ID
- App group: $APP_GROUP_ID
- Keychain group: $KEYCHAIN_GROUP_ID
- Network Extension entitlement: $NETWORK_EXTENSION_VALUE

PROOF

append_proof "## Source Entitlement Preflight"
verify_source_entitlements "iOS app" "$SOURCE_APP_ENTITLEMENTS"
verify_source_entitlements "packet tunnel" "$SOURCE_TUNNEL_ENTITLEMENTS"
verify_source_entitlements "iOS widget" "$SOURCE_WIDGET_ENTITLEMENTS"

write_build_settings_proof "ClambhookiOS"
write_build_settings_proof "ClambhookPacketTunnel"
write_build_settings_proof "ClambhookiOSWidgetExtension"

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

archive_app_path="$ARCHIVE_PATH/Products/Applications/ClambhookiOS.app"
append_proof "## Archive Product Verification"
verify_payload_tree "archive" "$archive_app_path"

ipa_root="$TMP_DIR/ipa"
mkdir -p "$ipa_root"
unzip -q "$ipa_path" -d "$ipa_root"

payload_app_count=0
payload_app_path=""
while IFS= read -r path; do
    payload_app_count=$((payload_app_count + 1))
    payload_app_path="$path"
done < <(find "$ipa_root/Payload" -maxdepth 1 -type d -name '*.app' -print)

if [[ "$payload_app_count" -ne 1 ]]; then
    fail "expected exactly one app bundle in exported IPA payload, found $payload_app_count."
fi

append_proof "## Exported IPA Verification"
append_proof "- IPA path: $ipa_path"
verify_payload_tree "exported IPA" "$payload_app_path"

echo "Created $ipa_path"
echo "Signing proof: $SIGNING_PROOF"
