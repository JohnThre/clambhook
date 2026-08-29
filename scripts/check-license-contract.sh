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
    5fa31fc5312f2c56d3ff008e5a3162ba6359ff9db9dde3db0a8cbae701cb843a \
    '{"command":"commercial-terms"}'
check_fixture ensure-trial \
    700aeed8b8d5d65f52a5e03996d601cd219d1207098e4deabbb7af28754cb822 \
    '{"command":"ensure-trial","snapshot":"","nowUnixMillis":1780444800123}'
check_fixture evaluate-trial \
    453dfc8b2f23626aed7dcc349c0b368f0d3fb3ecdaf647ce0ffa47dcafbe9091 \
    '{"command":"evaluate","snapshot":"{\"trialStartDate\":\"2026-01-31T00:00:00Z\",\"transactions\":null,\"lastVerifiedAt\":null,\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-01-31T00:00:00Z\"}","nowUnixMillis":1772150400000}'
check_fixture evaluate-lifetime \
    fb3758ab7fff4e2f3aabe4d9c83b47336259a71a6953501223a9ddb07b64b67f \
    '{"command":"evaluate","snapshot":"{\"trialStartDate\":null,\"transactions\":[{\"productID\":\"org.jpfchang.clambhook.unlock.lifetime\",\"purchaseDate\":\"2026-06-03T00:00:00Z\"},{\"productID\":\"org.jpfchang.clambhook.feature_update.2027\",\"purchaseDate\":\"2027-08-01T00:00:00Z\"}],\"lastVerifiedAt\":\"2027-08-01T00:00:00Z\",\"lastVerificationFailedAt\":null,\"cachedAt\":\"2027-08-01T00:00:00Z\"}","nowUnixMillis":1817251200000}'
check_fixture status-trial \
    0c19a4e7fa104f871947324917691bf8adca95926af3e35fb4a99a6247d8f30a \
    '{"command":"status","snapshot":"{\"trialStartDate\":\"2026-06-03T00:00:00Z\",\"transactions\":null,\"lastVerifiedAt\":null,\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-06-03T00:00:00Z\"}","nowUnixMillis":1781049600000}'
check_fixture status-expired-trial \
    48dc42f2fce3587f61cedde117e95af0f7dcbec66ce56145650f1b3fa244277b \
    '{"command":"status","snapshot":"{\"trialStartDate\":\"2026-06-03T00:00:00Z\",\"transactions\":null,\"lastVerifiedAt\":null,\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-06-03T00:00:00Z\"}","nowUnixMillis":1785888000000}'
check_fixture status-expired-updates \
    94e00f2e6ada19c558c3ef9a4b7855d58794d8dc3a5410f640991e565f70db70 \
    '{"command":"status","snapshot":"{\"trialStartDate\":null,\"transactions\":[{\"productID\":\"org.jpfchang.clambhook.unlock.lifetime\",\"purchaseDate\":\"2026-06-03T00:00:00Z\"}],\"lastVerifiedAt\":\"2026-06-03T00:00:00Z\",\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-06-03T00:00:00Z\"}","updatePublishedAtMillis":1812067200000,"nowUnixMillis":1812067200000}'
check_fixture invalid-device-action \
    85e454eccb567bc263278b44c5187245c3f19c9008299b7cfddecd25a7a938a4 \
    '{"command":"device-action","action":"replace","deviceRegistration":"{}"}'
check_fixture update-allowed \
    2e02bfc2d23878c0ed458d30625440c496b4a09619c4a76b133ef63928ae1b94 \
    '{"command":"update-allowed","snapshot":"{\"trialStartDate\":null,\"transactions\":[{\"productID\":\"org.jpfchang.clambhook.unlock.lifetime\",\"purchaseDate\":\"2026-06-03T00:00:00Z\"}],\"lastVerifiedAt\":null,\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-06-03T00:00:00Z\"}","publishedAtMillis":1811980800000,"nowUnixMillis":1811980800000}'
check_fixture verification-failure \
    f4b4181ad17c10f9e8bb2e89d111570128e7caeede0403b892aee2c5da92c671 \
    '{"command":"mark-verification-failure","snapshot":"{\"trialStartDate\":null,\"transactions\":[{\"productID\":\"org.jpfchang.clambhook.unlock.lifetime\",\"purchaseDate\":\"2026-06-03T00:00:00Z\"}],\"lastVerifiedAt\":\"2026-07-01T00:00:00Z\",\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-07-01T00:00:00Z\"}","nowUnixMillis":1782950400000}'
