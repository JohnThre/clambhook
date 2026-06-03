#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLIST_BUDDY="/usr/libexec/PlistBuddy"
REQUIRE_DEMO_SECRET=0

EXPECTED_APP_GROUP="group.org.jpfchang.clambhook"
EXPECTED_KEYCHAIN_GROUP='$(AppIdentifierPrefix)org.jpfchang.clambhook'
EXPECTED_NETWORK_EXTENSION="packet-tunnel-provider"
DEMO_TEMPLATE="$ROOT_DIR/docs/app-store/app-review-demo-profile.toml.template"
DEMO_PASSWORD_PLACEHOLDER="__CLAMBHOOK_APP_REVIEW_DEMO_PASSWORD__"

usage() {
    cat <<USAGE
Usage: scripts/app-review-compliance-check.sh [--require-demo-secret]

Checks App Review compliance files, source entitlements, and the non-secret
demo profile template. When --require-demo-secret is set, the script also
requires CLAMBHOOK_APP_REVIEW_DEMO_PASSWORD and validates a temp-rendered demo
profile without writing the secret into the repository.
USAGE
}

fail() {
    echo "$1" >&2
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --require-demo-secret)
            REQUIRE_DEMO_SECRET=1
            shift
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            usage >&2
            fail "unknown argument: $1"
            ;;
    esac
done

require_file() {
    local path="$1"
    [[ -f "$path" ]] || fail "required file is missing: $path"
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "$1 is required for App Review compliance checks."
}

require_text() {
    local path="$1"
    local pattern="$2"
    local label="$3"
    if ! grep -Fq "$pattern" "$path"; then
        fail "$label is missing expected text: $pattern"
    fi
}

reject_text() {
    local path="$1"
    local pattern="$2"
    local label="$3"
    if grep -Fiq "$pattern" "$path"; then
        fail "$label contains stale or prohibited text: $pattern"
    fi
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

verify_source_entitlements() {
    local label="$1"
    local path="$2"

    require_file "$path"
    plist_array_contains "$path" "com.apple.security.application-groups" "$EXPECTED_APP_GROUP" "$label entitlements"
    plist_array_contains "$path" "keychain-access-groups" "$EXPECTED_KEYCHAIN_GROUP" "$label entitlements"
    plist_array_contains "$path" "com.apple.developer.networking.networkextension" "$EXPECTED_NETWORK_EXTENSION" "$label entitlements"
}

render_demo_profile() {
    local output="$1"
    local password="${CLAMBHOOK_APP_REVIEW_DEMO_PASSWORD:-}"

    [[ -n "$password" ]] || fail "CLAMBHOOK_APP_REVIEW_DEMO_PASSWORD must be set to validate the App Review demo profile."
    if [[ "$password" == *$'\n'* || "$password" == *$'\r'* ]]; then
        fail "CLAMBHOOK_APP_REVIEW_DEMO_PASSWORD must be a single-line value."
    fi

    python3 - "$DEMO_TEMPLATE" "$output" "$DEMO_PASSWORD_PLACEHOLDER" "$password" <<'PY'
import pathlib
import sys

template_path, output_path, placeholder, password = sys.argv[1:]
template = pathlib.Path(template_path).read_text(encoding="utf-8")
if placeholder not in template:
    raise SystemExit(f"demo template is missing placeholder {placeholder}")
escaped = password.replace("\\", "\\\\").replace('"', '\\"')
pathlib.Path(output_path).write_text(template.replace(placeholder, escaped), encoding="utf-8")
PY
}

run_go_demo_validation() {
    local config_path="$1"

    if [[ -n "${DEMO_GO_CACHE:-}" ]]; then
        GOCACHE="$DEMO_GO_CACHE" CLAMBHOOK_APP_REVIEW_DEMO_CONFIG="$config_path" go test ./pkg/mobile -run TestValidateAppReviewDemoProfile -count=1
        return
    fi
    CLAMBHOOK_APP_REVIEW_DEMO_CONFIG="$config_path" go test ./pkg/mobile -run TestValidateAppReviewDemoProfile -count=1
}

require_command grep
require_command sed

if [[ ! -x "$PLIST_BUDDY" ]]; then
    fail "$PLIST_BUDDY is required to verify Apple entitlement plists."
fi

require_file "$DEMO_TEMPLATE"
require_text "$DEMO_TEMPLATE" "name = \"App Review Demo\"" "demo profile template"
require_text "$DEMO_TEMPLATE" "address = \"review-vpn.jpfchang.org:443\"" "demo profile template"
require_text "$DEMO_TEMPLATE" "protocol = \"clambback\"" "demo profile template"
require_text "$DEMO_TEMPLATE" "password = \"$DEMO_PASSWORD_PLACEHOLDER\"" "demo profile template"
reject_text "$DEMO_TEMPLATE" "hunter2" "demo profile template"
reject_text "$DEMO_TEMPLATE" "secret-token" "demo profile template"

require_text "$ROOT_DIR/docs/app-store/metadata-en-US.md" "premium access and paid feature updates" "App Store metadata"
require_text "$ROOT_DIR/docs/app-store/metadata-en-US.md" "v1 inspection is metadata-only" "App Store metadata"
require_text "$ROOT_DIR/docs/app-store/metadata-en-US.md" "United States only" "App Store metadata"
reject_text "$ROOT_DIR/docs/app-store/metadata-en-US.md" "support purchases do not unlock features" "App Store metadata"
reject_text "$ROOT_DIR/docs/app-store/metadata-en-US.md" "Support purchases do not unlock features" "App Store metadata"

require_text "$ROOT_DIR/docs/app-store/review-notes.md" "ClambHook does not sell, use, or disclose VPN traffic data to third parties." "App Review notes"
require_text "$ROOT_DIR/docs/app-store/review-notes.md" "v1 inspection is metadata-only" "App Review notes"
require_text "$ROOT_DIR/docs/app-store/review-notes.md" "Territory plan: \`docs/app-store/territory-plan.md\`" "App Review notes"
reject_text "$ROOT_DIR/docs/app-store/review-notes.md" "support purchases do not unlock features" "App Review notes"

require_text "$ROOT_DIR/docs/app-store/privacy.md" "does not sell, use, or disclose VPN traffic data to third parties" "privacy policy"
require_text "$ROOT_DIR/docs/app-store/territory-plan.md" "United States only" "territory plan"
require_text "$ROOT_DIR/docs/distribution.md" "Premium access and paid feature updates are sold through non-consumable In-App Purchases." "distribution policy"

verify_source_entitlements "iOS app" "$ROOT_DIR/ui/apple/ClambhookiOS/ClambhookiOS.entitlements"
verify_source_entitlements "packet tunnel" "$ROOT_DIR/ui/apple/ClambhookPacketTunnel/ClambhookPacketTunnel.entitlements"
verify_source_entitlements "iOS widget" "$ROOT_DIR/ui/apple/ClambhookWidgets/ClambhookiOSWidgetExtension.entitlements"

if [[ "$REQUIRE_DEMO_SECRET" -eq 1 || -n "${CLAMBHOOK_APP_REVIEW_DEMO_PASSWORD:-}" ]]; then
    require_command python3
    require_command go

    tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-app-review.XXXXXX")"
    cleanup() {
        rm -rf "$tmp_dir"
    }
    trap cleanup EXIT

    rendered="$tmp_dir/app-review-demo.toml"
    DEMO_GO_CACHE="$tmp_dir/go-build"
    render_demo_profile "$rendered"
    run_go_demo_validation "$rendered"
else
    echo "Skipping rendered demo profile validation; CLAMBHOOK_APP_REVIEW_DEMO_PASSWORD is not set."
fi

echo "App Review compliance check passed."
