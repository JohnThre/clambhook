#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NATIVE_HELPER="${1:-$ROOT_DIR/build-native/clambhook-license-c}"

if [[ ! -x "$NATIVE_HELPER" ]]; then
    echo "native license helper not found: $NATIVE_HELPER" >&2
    exit 2
fi

PARITY_TMP="$(mktemp -d)"
trap 'rm -rf "$PARITY_TMP"' EXIT
export GOCACHE="$PARITY_TMP/go-cache"
export GOMODCACHE="$PARITY_TMP/go-mod-cache"
export GOTMPDIR="$PARITY_TMP"

GO_HELPER="$PARITY_TMP/clambhook-license-go"
(cd "$ROOT_DIR" && go build -mod=vendor -o "$GO_HELPER" ./cmd/clambhook-license)

compare() {
    local name="$1"
    local request="$2"
    local go_output native_output
    go_output="$(printf '%s\n' "$request" | "$GO_HELPER")"
    native_output="$(printf '%s\n' "$request" | "$NATIVE_HELPER")"
    if [[ "$native_output" != "$go_output" ]]; then
        echo "license parity failed: $name" >&2
        printf 'Go:     %s\n' "$go_output" >&2
        printf 'Native: %s\n' "$native_output" >&2
        return 1
    fi
    printf 'license parity passed: %s\n' "$name"
}

compare commercial-terms '{"command":"commercial-terms"}'
compare ensure-trial '{"command":"ensure-trial","snapshot":"","nowUnixMillis":1780444800123}'
compare evaluate-trial '{"command":"evaluate","snapshot":"{\"trialStartDate\":\"2026-01-31T00:00:00Z\",\"transactions\":null,\"lastVerifiedAt\":null,\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-01-31T00:00:00Z\"}","nowUnixMillis":1772150400000}'
compare evaluate-lifetime '{"command":"evaluate","snapshot":"{\"trialStartDate\":null,\"transactions\":[{\"productID\":\"org.jpfchang.clambhook.unlock.lifetime\",\"purchaseDate\":\"2026-06-03T00:00:00Z\"},{\"productID\":\"org.jpfchang.clambhook.feature_update.2027\",\"purchaseDate\":\"2027-08-01T00:00:00Z\"}],\"lastVerifiedAt\":\"2027-08-01T00:00:00Z\",\"lastVerificationFailedAt\":null,\"cachedAt\":\"2027-08-01T00:00:00Z\"}","nowUnixMillis":1817251200000}'
compare status-trial '{"command":"status","snapshot":"{\"trialStartDate\":\"2026-06-03T00:00:00Z\",\"transactions\":null,\"lastVerifiedAt\":null,\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-06-03T00:00:00Z\"}","nowUnixMillis":1781049600000}'
compare status-expired-trial '{"command":"status","snapshot":"{\"trialStartDate\":\"2026-06-03T00:00:00Z\",\"transactions\":null,\"lastVerifiedAt\":null,\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-06-03T00:00:00Z\"}","nowUnixMillis":1785888000000}'
compare status-expired-updates '{"command":"status","snapshot":"{\"trialStartDate\":null,\"transactions\":[{\"productID\":\"org.jpfchang.clambhook.unlock.lifetime\",\"purchaseDate\":\"2026-06-03T00:00:00Z\"}],\"lastVerifiedAt\":\"2026-06-03T00:00:00Z\",\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-06-03T00:00:00Z\"}","updatePublishedAtMillis":1812067200000,"nowUnixMillis":1812067200000}'
compare invalid-device-action '{"command":"device-action","action":"replace","deviceRegistration":"{}"}'
compare update-allowed '{"command":"update-allowed","snapshot":"{\"trialStartDate\":null,\"transactions\":[{\"productID\":\"org.jpfchang.clambhook.unlock.lifetime\",\"purchaseDate\":\"2026-06-03T00:00:00Z\"}],\"lastVerifiedAt\":null,\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-06-03T00:00:00Z\"}","publishedAtMillis":1811980800000,"nowUnixMillis":1811980800000}'
compare verification-failure '{"command":"mark-verification-failure","snapshot":"{\"trialStartDate\":null,\"transactions\":[{\"productID\":\"org.jpfchang.clambhook.unlock.lifetime\",\"purchaseDate\":\"2026-06-03T00:00:00Z\"}],\"lastVerifiedAt\":\"2026-07-01T00:00:00Z\",\"lastVerificationFailedAt\":null,\"cachedAt\":\"2026-07-01T00:00:00Z\"}","nowUnixMillis":1782950400000}'
