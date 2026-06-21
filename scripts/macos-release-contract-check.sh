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
recovery="$ROOT_DIR/ui/apple/Sources/ClambhookShared/AppRecoveryState.swift"
app_model="$ROOT_DIR/ui/apple/SharedApp/AppleAppModel.swift"
purchase_view="$ROOT_DIR/ui/apple/SharedApp/SupportPurchasesView.swift"
mobile_support="$ROOT_DIR/ui/apple/Sources/ClambhookShared/MobileSupport.swift"
product_fixture="$ROOT_DIR/ui/apple/ClambhookProducts.json"
commercial_setup="$ROOT_DIR/docs/app-store/commercial-setup.md"
metadata_en_us="$ROOT_DIR/docs/app-store/metadata-en-US.md"
review_notes="$ROOT_DIR/docs/app-store/review-notes.md"
privacy="$ROOT_DIR/docs/app-store/privacy.md"
license_validation="$ROOT_DIR/docs/license-validation.md"

for path in \
    "$distribution" \
    "$readme" \
    "$commercial_setup" \
    "$metadata_en_us" \
    "$review_notes" \
    "$privacy" \
    "$license_validation" \
    "$settings" \
    "$licensing" \
    "$recovery" \
    "$app_model" \
    "$purchase_view" \
    "$mobile_support" \
    "$product_fixture"; do
    require_file "$path"
done

require_text "$readme" "distributed only from \`https://jpfchang.org/clambhook/\`" "README distribution policy"
product_promise="USD 99.99"
versions_promise="during that year remain usable"
device_promise="up to 4 active"
transfer_promise="transferable"

require_text "$readme" "USD 99.99 macOS license includes one year of feature updates" "README license policy"
require_text "$readme" "$versions_promise" "README version-usability policy"
require_text "$readme" "$device_promise" "README device policy"
require_text "$readme" "$transfer_promise" "README transfer policy"

require_text "$distribution" "A USD 99.99 direct-sale macOS license includes one year of feature updates" "distribution policy"
require_text "$distribution" "$versions_promise" "distribution policy"
require_text "$distribution" "A USD 8.99 paid feature update unlocks new features released after the included first year" "distribution policy"
require_text "$distribution" "License device listing, activation, deactivation, and transfer" "distribution policy"
require_text "$distribution" "ClambHook for macOS License" "distribution products"
require_text "$distribution" "Bug fixes and security fixes remain included" "distribution update policy"
reject_text "$distribution" "In-App Purchase" "distribution policy"
reject_text "$distribution" "StoreKit" "distribution policy"
reject_text "$distribution" "App Store" "distribution policy"
reject_text "$distribution" "lifetime license" "distribution policy"
reject_text "$distribution" "lifetime unlock" "distribution policy"

for public_copy_file in "$commercial_setup" "$metadata_en_us" "$review_notes" "$privacy" "$license_validation"; do
    require_text "$public_copy_file" "$product_promise" "public product promise"
    require_text "$public_copy_file" "$versions_promise" "public version-usability promise"
    require_text "$public_copy_file" "$device_promise" "public device promise"
    require_text "$public_copy_file" "$transfer_promise" "public transfer promise"
    reject_text "$public_copy_file" "Lifetime license" "public product copy"
    reject_text "$public_copy_file" "lifetime license" "public product copy"
done

require_text "$settings" "https://jpfchang.org/api/clambhook/update-manifest" "stable update manifest"
require_text "$settings" "https://jpfchang.org/api/clambhook/update-manifest?channel=beta" "beta update manifest"

for ui_copy_file in "$licensing" "$recovery" "$app_model" "$purchase_view" "$mobile_support"; do
    reject_text "$ui_copy_file" "StoreKit" "macOS website license UI copy"
    reject_text "$ui_copy_file" "App Store purchase" "macOS website license UI copy"
    reject_text "$ui_copy_file" "Unlock Lifetime" "macOS website license UI copy"
    reject_text "$ui_copy_file" "lifetime unlock" "macOS website license UI copy"
    reject_text "$ui_copy_file" "Lifetime Unlock" "macOS website license UI copy"
done

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

expected = [
    {
        "productID": "org.jpfchang.clambhook.unlock.lifetime",
        "kind": "license",
        "displayPrice": "99.99",
        "displayName": "ClambHook macOS License",
        "description": "USD 99.99 includes one year of feature updates; versions released during that year remain usable; up to 4 active devices; transferable.",
    },
    {
        "productID": "org.jpfchang.clambhook.feature_update.2027",
        "kind": "feature_update",
        "displayPrice": "8.99",
        "displayName": "ClambHook 2027 Feature Update",
        "description": "Extends the ClambHook macOS feature-update window by one year.",
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
