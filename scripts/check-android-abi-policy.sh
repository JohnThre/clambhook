#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GRADLE_FILE="$ROOT_DIR/ui/android/app/build.gradle.kts"
WORKFLOW_FILE="$ROOT_DIR/.github/workflows/ci.yml"
DEBUG_AAR="$ROOT_DIR/ui/android/app/build/outputs/aar/clambhook-android-platform-debug.aar"
RELEASE_AAR="$ROOT_DIR/ui/android/app/build/outputs/aar/clambhook-android-platform-release.aar"
REQUIRE_DEBUG=0
REQUIRE_RELEASE=0

for argument in "$@"; do
    case "$argument" in
        --require-debug) REQUIRE_DEBUG=1 ;;
        --require-release) REQUIRE_RELEASE=1 ;;
        *)
            echo "usage: $0 [--require-debug] [--require-release]" >&2
            exit 2
            ;;
    esac
done

fail() {
    echo "android ABI policy: $1" >&2
    exit 1
}

grep -Fq 'abiFilters += "arm64-v8a"' "$GRADLE_FILE" ||
    fail "the production ABI filter is not ARM64"
grep -Fq 'managedDeviceAbi == null || managedDeviceAbi == "x86_64"' "$GRADLE_FILE" ||
    fail "the debug managed-device ABI is not restricted to x86_64"
grep -Fq "'system-images;android-\${{ matrix.api }};aosp_atd;x86_64'" "$WORKFLOW_FILE" ||
    fail "hosted managed devices do not use the x86_64 ATD image"
grep -Fq -- '-Pclambhook.android.managedDeviceAbi=x86_64' "$WORKFLOW_FILE" ||
    fail "hosted managed devices do not request the x86_64 debug JNI slice"
if grep -Fq 'aosp_atd;arm64-v8a' "$WORKFLOW_FILE"; then
    fail "the hosted workflow still requests an ARM64 ATD image"
fi

inspect_aar() {
    local archive="$1"
    local expected="$2"
    local label="$3"
    local actual

    command -v unzip >/dev/null 2>&1 || fail "unzip is required to inspect $label"
    actual="$(unzip -Z1 "$archive" | awk -F/ '$1 == "jni" && $2 != "" { print $2 }' | sort -u)"
    [[ "$actual" == "$expected" ]] || {
        printf '%s ABI set was:\n%s\n' "$label" "${actual:-<empty>}" >&2
        fail "$label has an unexpected native ABI set"
    }
}

if [[ -f "$DEBUG_AAR" ]]; then
    inspect_aar "$DEBUG_AAR" $'arm64-v8a\nx86_64' "debug AAR"
elif (( REQUIRE_DEBUG )); then
    fail "required debug AAR is missing"
fi

if [[ -f "$RELEASE_AAR" ]]; then
    inspect_aar "$RELEASE_AAR" 'arm64-v8a' "release AAR"
elif (( REQUIRE_RELEASE )); then
    fail "required release AAR is missing"
fi

echo "android ABI policy: all checks passed"
