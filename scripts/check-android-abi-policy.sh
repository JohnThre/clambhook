#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GRADLE_FILE="$ROOT_DIR/ui/kotlin/platform/build.gradle.kts"
APP_GRADLE_FILE="$ROOT_DIR/ui/kotlin/app/build.gradle.kts"
WORKFLOW_FILE="$ROOT_DIR/.github/workflows/ci.yml"
DEBUG_AAR="$ROOT_DIR/ui/kotlin/platform/build/outputs/aar/clambhook-android-platform-debug.aar"
RELEASE_AAR="$ROOT_DIR/ui/kotlin/platform/build/outputs/aar/clambhook-android-platform-release.aar"
RELEASE_APK="$ROOT_DIR/ui/kotlin/app/build/outputs/apk/release/app-release.apk"
# Without the release keystore, AGP names the local output *-unsigned.apk.
[[ -f "$RELEASE_APK" ]] || RELEASE_APK="${RELEASE_APK%.apk}-unsigned.apk"
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
grep -Fq 'abiFilters += "arm64-v8a"' "$APP_GRADLE_FILE" ||
    fail "the application ABI filter is not ARM64"

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

if [[ -f "$RELEASE_APK" ]]; then
    actual="$(unzip -Z1 "$RELEASE_APK" | awk -F/ '$1 == "lib" && $2 != "" { print $2 }' | sort -u)"
    [[ "$actual" == "arm64-v8a" ]] || {
        printf 'release APK ABI set was:\n%s\n' "${actual:-<empty>}" >&2
        fail "release APK has an unexpected native ABI set"
    }
fi

echo "android ABI policy: all checks passed"
