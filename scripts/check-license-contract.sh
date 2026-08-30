#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HELPER="${1:-$ROOT_DIR/build-native/clambhook-license}"

if [[ ! -x "$HELPER" ]]; then
    echo "license helper not found: $HELPER" >&2
    exit 2
fi

sha256_text() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum | awk '{print $1}'
    else
        shasum -a 256 | awk '{print $1}'
    fi
}

check_fixture() {
    local name="$1"
    local expected_sha256="$2"
    local request="$3"
    local output actual_sha256
    output="$(printf '%s\n' "$request" | "$HELPER")"
    actual_sha256="$(printf '%s\n' "$output" | sha256_text)"
    if [[ "$actual_sha256" != "$expected_sha256" ]]; then
        echo "license contract failed: $name" >&2
        printf 'expected sha256: %s\nactual sha256:   %s\noutput: %s\n' \
            "$expected_sha256" "$actual_sha256" "$output" >&2
        return 1
    fi
    printf 'license contract passed: %s\n' "$name"
}

# These checksums freeze the exact JSON line protocol formerly verified
# differentially during the C cutover. Inputs remain explicit so a contract
# change requires an intentional fixture review.
check_fixture commercial-terms \
    2dc2d5caa3ba8d369959caf65ccfe734c3f76142578d5b81babc2c9104b4dab9 \
    '{"command":"commercial-terms"}'
check_fixture ensure-trial \
    64a58952f44e08c54a0dfe01bb288389ba96636f3891c571eae4bf974a4fe086 \
    '{"command":"ensure-trial","snapshot":"","nowUnixMillis":1780444800123}'
check_fixture evaluate-trial \
    cac0c575fa2924356b501490237e497d45f663ec7a728aa83300c69a42e233f8 \
    '{"command":"evaluate","snapshot":"{\"trialStartDate\":\"2026-01-31T00:00:00Z\",\"transactions\":null,\"lastVerifiedAt\":null,\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-01-31T00:00:00Z\"}","nowUnixMillis":1772150400000}'
check_fixture evaluate-lifetime \
    90f7be5c6677e57c8fdc0021e2313e3654aa318fad0e97ae87fbd42e5ee7a301 \
    '{"command":"evaluate","snapshot":"{\"trialStartDate\":null,\"transactions\":[{\"productID\":\"org.jpfchang.clambhook.unlock.lifetime\",\"purchaseDate\":\"2026-06-03T00:00:00Z\"},{\"productID\":\"org.jpfchang.clambhook.feature_update.2027\",\"purchaseDate\":\"2027-08-01T00:00:00Z\"}],\"lastVerifiedAt\":\"2027-08-01T00:00:00Z\",\"lastVerificationFailedAt\":null,\"cachedAt\":\"2027-08-01T00:00:00Z\"}","nowUnixMillis":1817251200000}'
check_fixture status-trial \
    e21c5291e31d8702fdb6d532224ab64e11b531c9b29f5b0dde059ed75bfde9bb \
    '{"command":"status","snapshot":"{\"trialStartDate\":\"2026-06-03T00:00:00Z\",\"transactions\":null,\"lastVerifiedAt\":null,\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-06-03T00:00:00Z\"}","nowUnixMillis":1781049600000}'
check_fixture status-expired-trial \
    c1ea46003edf3b3760f89e66074ab0809e2f1a8ad27459e7603d1d9d4c83f426 \
    '{"command":"status","snapshot":"{\"trialStartDate\":\"2026-06-03T00:00:00Z\",\"transactions\":null,\"lastVerifiedAt\":null,\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-06-03T00:00:00Z\"}","nowUnixMillis":1785888000000}'
check_fixture status-expired-updates \
    31b6f23e264b5a7f913fc84924727b04103660572c6e6a997d33c0a2f70509ec \
    '{"command":"status","snapshot":"{\"trialStartDate\":null,\"transactions\":[{\"productID\":\"org.jpfchang.clambhook.unlock.lifetime\",\"purchaseDate\":\"2026-06-03T00:00:00Z\"}],\"lastVerifiedAt\":\"2026-06-03T00:00:00Z\",\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-06-03T00:00:00Z\"}","updatePublishedAtMillis":1812067200000,"nowUnixMillis":1812067200000}'
check_fixture invalid-device-action \
    85e454eccb567bc263278b44c5187245c3f19c9008299b7cfddecd25a7a938a4 \
    '{"command":"device-action","action":"replace","deviceRegistration":"{}"}'
check_fixture update-allowed \
    2e02bfc2d23878c0ed458d30625440c496b4a09619c4a76b133ef63928ae1b94 \
    '{"command":"update-allowed","snapshot":"{\"trialStartDate\":null,\"transactions\":[{\"productID\":\"org.jpfchang.clambhook.unlock.lifetime\",\"purchaseDate\":\"2026-06-03T00:00:00Z\"}],\"lastVerifiedAt\":null,\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-06-03T00:00:00Z\"}","publishedAtMillis":1811980800000,"nowUnixMillis":1811980800000}'
check_fixture verification-failure \
    896619344ea24197919d8ae935b595d59fed6d47cde6f5bcbd1de704bf817a92 \
    '{"command":"mark-verification-failure","snapshot":"{\"trialStartDate\":null,\"transactions\":[{\"productID\":\"org.jpfchang.clambhook.unlock.lifetime\",\"purchaseDate\":\"2026-06-03T00:00:00Z\"}],\"lastVerifiedAt\":\"2026-07-01T00:00:00Z\",\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-07-01T00:00:00Z\"}","nowUnixMillis":1782950400000}'
