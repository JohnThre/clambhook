#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
    echo "$1" >&2
    exit 1
}

require_file() {
    local path="$1"
    [[ -f "$path" ]] || fail "required file is missing: $path"
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "$1 is required for macOS release contract checks."
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


require_command grep
require_command python3

distribution="$ROOT_DIR/docs/distribution.md"
readme="$ROOT_DIR/README.md"
settings="$ROOT_DIR/ui/apple/Sources/ClambhookShared/Settings.swift"
licensing="$ROOT_DIR/ui/apple/Sources/ClambhookShared/Licensing.swift"
license_devices="$ROOT_DIR/ui/apple/Sources/ClambhookShared/LicenseDeviceModels.swift"
recovery="$ROOT_DIR/ui/apple/Sources/ClambhookShared/AppRecoveryState.swift"
app_model="$ROOT_DIR/ui/apple/SharedApp/AppleAppModel.swift"
sparkle_updater="$ROOT_DIR/ui/apple/ClambhookMac/MacSparkleUpdater.swift"
purchase_view="$ROOT_DIR/ui/apple/SharedApp/SupportPurchasesView.swift"
mobile_support="$ROOT_DIR/ui/apple/Sources/ClambhookShared/MobileSupport.swift"
product_fixture="$ROOT_DIR/ui/apple/ClambhookProducts.json"
website_release_dir="$ROOT_DIR/docs/website-release"
commercial_setup="$website_release_dir/commercial-setup.md"
product_copy_en_us="$website_release_dir/product-copy-en-US.md"
copy_notes="$website_release_dir/copy-notes.md"
privacy="$website_release_dir/privacy.md"
signing="$website_release_dir/signing.md"
support="$website_release_dir/support.md"
availability_plan="$website_release_dir/availability-plan.md"
support_demo_profile="$website_release_dir/support-demo-profile.toml.template"
license_validation="$ROOT_DIR/docs/license-validation.md"

for path in \
    "$distribution" \
    "$readme" \
    "$commercial_setup" \
    "$product_copy_en_us" \
    "$copy_notes" \
    "$privacy" \
    "$signing" \
    "$support" \
    "$availability_plan" \
    "$support_demo_profile" \
    "$license_validation" \
    "$settings" \
    "$licensing" \
    "$license_devices" \
    "$recovery" \
    "$app_model" \
    "$sparkle_updater" \
    "$purchase_view" \
    "$mobile_support" \
    "$product_fixture"; do
    require_file "$path"
done

require_text "$readme" "distributed only from \`https://store.clambercloud.com/clambhook/\`" "README distribution policy"
require_text "$readme" "free public DMG download for Apple Silicon Macs running macOS 14 or later" "README macOS availability policy"
product_promise="USD 99.99"
versions_promise="on or before the update cutoff remain usable"
device_promise="maximum of 10 concurrently active"
transfer_promise="deactivated"

require_text "$readme" "USD 99.99 one-time ClambHook license" "README license policy"
require_text "$readme" "$versions_promise" "README version-usability policy"
require_text "$readme" "$device_promise" "README device policy"
require_text "$readme" "$transfer_promise" "README transfer policy"
require_text "$readme" "one year of all updates" "README included-update policy"
require_text "$readme" "critical, bug, and security updates" "README strict update-cutoff policy"
require_text "$readme" "Creem or NOWPayments, not PayPal" "README payment-provider policy"

require_text "$distribution" "A USD 99.99 one-time ClambHook license is required after the trial and includes one year of all updates" "distribution policy"
require_text "$distribution" "$versions_promise" "distribution policy"
require_text "$distribution" "A USD 9.99 renewal buys one additional update year" "distribution policy"
require_text "$distribution" "free and supports Apple Silicon Macs running macOS 14.0 or later" "distribution macOS availability policy"
require_text "$distribution" "Device seats can be deactivated" "distribution policy"
require_text "$distribution" "ClambHook License" "distribution products"
require_text "$distribution" "critical, bug, and security updates" "distribution update policy"
require_text "$distribution" "Creem or NOWPayments, not PayPal" "distribution payment-provider policy"
reject_text "$distribution" "In-App Purchase" "distribution policy"
reject_text "$distribution" "StoreKit" "distribution policy"
reject_text "$distribution" "App Store" "distribution policy"
reject_text "$distribution" "App Review" "distribution policy"
reject_text "$distribution" "lifetime license" "distribution policy"
reject_text "$distribution" "lifetime unlock" "distribution policy"

for public_copy_file in "$commercial_setup" "$product_copy_en_us" "$copy_notes" "$privacy" "$license_validation"; do
    require_text "$public_copy_file" "$product_promise" "public product promise"
    require_text "$public_copy_file" "$versions_promise" "public version-usability promise"
    require_text "$public_copy_file" "$device_promise" "public device promise"
    require_text "$public_copy_file" "$transfer_promise" "public transfer promise"
    require_text "$public_copy_file" "one-calendar-month" "public trial promise"
    require_text "$public_copy_file" "one year of all updates" "public included-update promise"
    require_text "$public_copy_file" "critical, bug, and security updates" "public strict update-cutoff promise"
    require_text "$public_copy_file" "Creem" "public payment-provider promise"
    require_text "$public_copy_file" "NOWPayments" "public payment-provider promise"
    reject_text "$public_copy_file" "Lifetime license" "public product copy"
    reject_text "$public_copy_file" "lifetime license" "public product copy"
done

require_text "$privacy" "HTTP Capture is a separate local opt-in" "privacy capture disclosure"
require_text "$privacy" "user-trusted local certificate" "privacy HTTPS capture disclosure"
require_text "$product_copy_en_us" "Apple Silicon Macs running macOS 14 or later" "product copy macOS availability"
require_text "$product_copy_en_us" "HTTP Capture workflows" "product copy capture feature"
require_text "$copy_notes" "HTTP(S) capture is public for macOS v1" "copy notes capture policy"

for website_release_file in \
    "$commercial_setup" \
    "$product_copy_en_us" \
    "$copy_notes" \
    "$privacy" \
    "$signing" \
    "$support" \
    "$availability_plan" \
    "$support_demo_profile" \
    "$license_validation"; do
    reject_text "$website_release_file" "StoreKit" "macOS website release copy"
    reject_text "$website_release_file" "App Store" "macOS website release copy"
    reject_text "$website_release_file" "App Review" "macOS website release copy"
    reject_text "$website_release_file" "app-store" "macOS website release copy"
    reject_text "$website_release_file" "app-review" "macOS website release copy"
done

require_text "$settings" "https://store.clambercloud.com/api/clambhook/update-manifest" "stable update manifest"
require_text "$settings" "https://store.clambercloud.com/api/clambhook/update-manifest?channel=beta" "beta update manifest"
reject_text "$settings" "public let defaultStableUpdateManifestURL = URL(string: \"https://jpfchang.org/clambhook/clambhook-update-manifest.json\")!" "stable update manifest"
reject_text "$settings" "public let defaultBetaUpdateManifestURL = URL(string: \"https://jpfchang.org/clambhook/clambhook-beta-update-manifest.json\")!" "beta update manifest"

for ui_copy_file in "$licensing" "$recovery" "$app_model" "$purchase_view" "$mobile_support"; do
    reject_text "$ui_copy_file" "StoreKit" "macOS website license UI copy"
    reject_text "$ui_copy_file" "App Store purchase" "macOS website license UI copy"
    reject_text "$ui_copy_file" "App Review" "macOS website license UI copy"
    reject_text "$ui_copy_file" "Unlock Lifetime" "macOS website license UI copy"
    reject_text "$ui_copy_file" "lifetime unlock" "macOS website license UI copy"
    reject_text "$ui_copy_file" "Lifetime Unlock" "macOS website license UI copy"
done

require_text "$licensing" "One-calendar-month trial" "macOS website license UI copy"
require_text "$licensing" "including critical, bug, and security updates" "macOS strict update-cutoff UI copy"
require_text "$license_devices" "case creem" "macOS Creem provider policy"
require_text "$license_devices" "case nowPayments" "macOS NOWPayments provider policy"
reject_text "$license_devices" "case manual" "macOS payment-provider policy"
require_text "$recovery" "Trial ended" "macOS website license recovery copy"
require_text "$recovery" "including critical, bug, and security updates" "macOS strict update-cutoff recovery copy"
require_text "$app_model" "one-calendar-month trial has ended" "macOS website license app copy"
require_text "$app_model" "MobileLicenseUpdatePolicy.canInstallUpdate" "macOS update gating"
reject_text "$sparkle_updater" "isCriticalUpdate ||" "Sparkle critical-update bypass"
require_text "$sparkle_updater" "including critical, bug, and security updates" "Sparkle strict update-cutoff copy"
require_text "$purchase_view" "Buy license - USD" "macOS website license purchase copy"
require_text "$mobile_support" "ClambHook License" "macOS website license product copy"

"$ROOT_DIR/scripts/check-source-only.sh" "$ROOT_DIR"

python3 - "$product_fixture" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
config = json.loads(path.read_text(encoding="utf-8"))
if config.get("type") != "direct-sale":
    raise SystemExit("product fixture must use type=direct-sale")
if config.get("version") != 1:
    raise SystemExit("product fixture must use version=1")
if config.get("paymentProviders") != ["creem", "nowpayments"]:
    raise SystemExit("product fixture payment providers must be exactly Creem and NOWPayments")

expected = [
    {
        "productID": "org.jpfchang.clambhook.unlock.lifetime",
        "kind": "license",
        "displayPrice": "99.99",
        "displayName": "ClambHook License",
        "description": "USD 99.99 one-time ClambHook license after a one-calendar-month trial; includes one year of all updates; versions released on or before the cutoff remain usable; maximum 10 concurrently active devices; deactivatable and transferable.",
    },
    {
        "productID": "org.jpfchang.clambhook.feature_update",
        "kind": "feature_update",
        "displayPrice": "9.99",
        "displayName": "ClambHook Update Year",
        "description": "USD 9.99 buys one additional update year from the later of the current cutoff or renewal payment date.",
    },
]

products = config.get("products")
if not isinstance(products, list):
    raise SystemExit("product fixture must contain a products array")
if len(products) != len(expected):
    raise SystemExit(f"product fixture must contain exactly {len(expected)} products")

for product, want in zip(products, expected):
    for key, value in want.items():
        if product.get(key) != value:
            raise SystemExit(f"{want['productID']} has {key}={product.get(key)!r}, expected {value!r}")
PY

echo "macOS website release contract check passed."
