#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLIST_BUDDY="/usr/libexec/PlistBuddy"
APP_PATH="${1:-}"

EXPECTED_APP_ID="org.jpfchang.clambhook.mac"
EXPECTED_WIDGET_ID="org.jpfchang.clambhook.mac.widgets"
EXPECTED_HELPER_LABEL="org.jpfchang.clambhook.mac.helper"
EXPECTED_APP_GROUP="group.org.jpfchang.clambhook"
EXPECTED_KEYCHAIN_GROUP='$(AppIdentifierPrefix)org.jpfchang.clambhook'

fail() {
    echo "$1" >&2
    exit 1
}

require_file() {
    local path="$1"
    [[ -f "$path" ]] || fail "required file is missing: $path"
}

require_executable() {
    local path="$1"
    [[ -x "$path" ]] || fail "required executable is missing: $path"
}

plist_array_contains() {
    local plist="$1"
    local key="$2"
    local expected="$3"
    local label="$4"
    local values

    values="$("$PLIST_BUDDY" -c "Print :$key" "$plist" 2>/dev/null)" || \
        fail "$label is missing $key."
    if ! printf '%s\n' "$values" | sed 's/^[[:space:]]*//' | grep -Fxq "$expected"; then
        fail "$label $key does not include $expected."
    fi
}

plist_key_absent() {
    local plist="$1"
    local key="$2"
    local label="$3"

    if "$PLIST_BUDDY" -c "Print :$key" "$plist" >/dev/null 2>&1; then
        fail "$label must not include $key."
    fi
}

plist_string_equals() {
    local plist="$1"
    local key="$2"
    local expected="$3"
    local label="$4"
    local value

    value="$("$PLIST_BUDDY" -c "Print :$key" "$plist" 2>/dev/null)" || \
        fail "$label is missing $key."
    [[ "$value" == "$expected" ]] || fail "$label $key is $value, expected $expected."
}

check_source() {
    local app_entitlements="$ROOT_DIR/ui/apple/ClambhookMac/ClambhookMac.entitlements"
    local widget_entitlements="$ROOT_DIR/ui/apple/ClambhookWidgets/ClambhookMacWidgetExtension.entitlements"
    local helper_plist="$ROOT_DIR/ui/apple/ClambhookMacHelper/$EXPECTED_HELPER_LABEL.plist"
    local project="$ROOT_DIR/ui/apple/project.yml"

    require_file "$app_entitlements"
    require_file "$widget_entitlements"
    require_file "$helper_plist"
    require_file "$ROOT_DIR/ui/apple/ClambhookMacHelper/ClambhookMacHelper.entitlements"

    plist_array_contains "$app_entitlements" "com.apple.security.application-groups" "$EXPECTED_APP_GROUP" "macOS app entitlements"
    plist_array_contains "$app_entitlements" "keychain-access-groups" "$EXPECTED_KEYCHAIN_GROUP" "macOS app entitlements"
    plist_key_absent "$app_entitlements" "com.apple.developer.system-extension.install" "macOS app entitlements"
    plist_key_absent "$app_entitlements" "com.apple.developer.networking.networkextension" "macOS app entitlements"
    plist_array_contains "$widget_entitlements" "com.apple.security.application-groups" "$EXPECTED_APP_GROUP" "macOS widget entitlements"

    plist_string_equals "$helper_plist" "Label" "$EXPECTED_HELPER_LABEL" "helper launchd plist"
    plist_string_equals "$helper_plist" "BundleProgram" "Contents/Library/HelperTools/ClambhookMacHelper" "helper launchd plist"

    grep -Fq "PRODUCT_BUNDLE_IDENTIFIER: $EXPECTED_APP_ID" "$project" || fail "project.yml is missing $EXPECTED_APP_ID."
    grep -Fq "PRODUCT_BUNDLE_IDENTIFIER: $EXPECTED_WIDGET_ID" "$project" || fail "project.yml is missing $EXPECTED_WIDGET_ID."
    grep -Fq "PRODUCT_BUNDLE_IDENTIFIER: $EXPECTED_HELPER_LABEL" "$project" || fail "project.yml is missing $EXPECTED_HELPER_LABEL."
    ! grep -Fq "NetworkExtension.framework" "$project" || fail "project.yml must not depend on NetworkExtension.framework."
    ! grep -Fq "SystemExtensions.framework" "$project" || fail "project.yml must not depend on SystemExtensions.framework."
    if [[ -f "$ROOT_DIR/ui/apple/Clambhook.xcodeproj/project.pbxproj" ]] && \
        grep -Fq "ClambhookMacHelper in Resources" "$ROOT_DIR/ui/apple/Clambhook.xcodeproj/project.pbxproj"; then
        fail "ClambhookMacHelper must not be copied into app Resources; it belongs in Contents/Library/HelperTools."
    fi
}

check_exported_app() {
    local app="$1"
    local helper="$app/Contents/Library/HelperTools/ClambhookMacHelper"
    local helper_plist="$app/Contents/Library/LaunchDaemons/$EXPECTED_HELPER_LABEL.plist"

    [[ -d "$app" ]] || fail "exported app is missing: $app"
    [[ ! -d "$app/Contents/Library/SystemExtensions" ]] || \
        fail "exported app must not embed system extensions."
    require_executable "$helper"
    require_file "$helper_plist"
    [[ ! -e "$app/Contents/Resources/ClambhookMacHelper" ]] || \
        fail "helper was copied into Contents/Resources; expected only Contents/Library/HelperTools."
    plist_string_equals "$helper_plist" "Label" "$EXPECTED_HELPER_LABEL" "exported helper launchd plist"
    plist_string_equals "$helper_plist" "BundleProgram" "Contents/Library/HelperTools/ClambhookMacHelper" "exported helper launchd plist"

    codesign --verify --strict --verbose=2 "$app" >/dev/null
    codesign --verify --strict --verbose=2 "$helper" >/dev/null
}

command -v grep >/dev/null 2>&1 || fail "grep is required."
command -v sed >/dev/null 2>&1 || fail "sed is required."
[[ -x "$PLIST_BUDDY" ]] || fail "$PLIST_BUDDY is required."

check_source
if [[ -n "$APP_PATH" ]]; then
    check_exported_app "$APP_PATH"
fi

echo "macOS signing/layout check passed."
