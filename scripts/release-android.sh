#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Build, inspect, checksum, and GPG-sign the Android ARM64 application.
# The protected release job supplies both the Android keystore and the GPG key.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

scripts/check-source-only.sh "$ROOT_DIR"
scripts/check-cutover.sh "$ROOT_DIR"
# shellcheck source=scripts/lib/release-gpg.sh
source "$ROOT_DIR/scripts/lib/release-gpg.sh"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)}"
VERSION_CODE="${VERSION_CODE:-3}"
UPDATE_CHANNEL="${UPDATE_CHANNEL:-stable}"
DIST_DIR="$ROOT_DIR/dist/android"
RELEASE_TAG="${RELEASE_TAG:-v${VERSION}}"
RELEASE_BASE="https://github.com/${GITHUB_REPOSITORY:-JohnThre/clambhook}/releases/download/${RELEASE_TAG}"
OUTPUT_DIR="$ROOT_DIR/ui/kotlin/app/build/outputs"

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

require() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "$1 is required for $2." >&2
        exit 2
    }
}

if [[ "${CLAMBHOOK_ANDROID_SKIP_BUILD:-0}" != "1" ]]; then
    if [[ -z "${CLAMBHOOK_ANDROID_KEYSTORE_PATH:-}" && "$REQUIRE_SIGNING" == "1" ]]; then
        echo "Android release signing credentials are required." >&2
        exit 2
    fi
    # app/build.gradle.kts reads VERSION, VERSION_CODE, and the
    # CLAMBHOOK_ANDROID_* keystore environment.
    (cd ui/kotlin && VERSION="$VERSION" VERSION_CODE="$VERSION_CODE" \
        ./gradlew --no-daemon :app:assembleRelease :app:bundleRelease)
fi

APK_SRC="$OUTPUT_DIR/apk/release/app-release.apk"
AAB_SRC="$OUTPUT_DIR/bundle/release/app-release.aab"
[[ -f "$APK_SRC" ]] || {
    echo "Android APK was not produced: $APK_SRC" >&2
    exit 1
}
[[ -f "$AAB_SRC" ]] || {
    echo "Android App Bundle was not produced: $AAB_SRC" >&2
    exit 1
}

APK="$DIST_DIR/ClambHook-arm64.apk"
AAB="$DIST_DIR/ClambHook-arm64.aab"
install -m 0644 "$APK_SRC" "$APK"
install -m 0644 "$AAB_SRC" "$AAB"

require unzip "Android artifact inspection"
if unzip -Z1 "$APK" | grep -E '^lib/' | grep -Ev '^lib/arm64-v8a/' | grep -q .; then
    echo "APK contains a non-ARM64 native payload." >&2
    exit 1
fi
unzip -Z1 "$APK" | grep -q '^lib/arm64-v8a/' || {
    echo "APK does not contain an ARM64 native payload." >&2
    exit 1
}
if unzip -Z1 "$APK" | grep -Eiq '(^|/)(javafx|gluon|gtk|jre|jdk)(/|\.|$)|\.go$'; then
    echo "APK contains a retired or prohibited payload." >&2
    exit 1
fi

if [[ "$REQUIRE_SIGNING" == "1" ]]; then
    APKSIGNER="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}/build-tools/36.0.0/apksigner"
    if [[ ! -x "$APKSIGNER" ]]; then
        APKSIGNER="$(command -v apksigner 2>/dev/null || true)"
    fi
    [[ -n "$APKSIGNER" && -x "$APKSIGNER" ]] || {
        echo "Android SDK build-tools 36.0.0 apksigner is required for APK signature verification." >&2
        exit 2
    }
    require jarsigner "App Bundle signature verification"
    "$APKSIGNER" verify --verbose --print-certs "$APK"
    jarsigner -verify -strict "$AAB"
fi

if ch_gpg_signing_enabled; then
    ch_gpg_check_expiry
fi
# Detached developer@jpfchang.org signatures for the installers themselves and
# their checksums; the in-app updater verifies the APK signature before install.
ch_checksum_and_sign "$APK"
ch_checksum_and_sign "$AAB"

MANIFEST="$DIST_DIR/clambhook-android-manifest.json"
PUBLISHED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
APK_SHA256="$(awk '{print $1}' "$APK.sha256")"
AAB_SHA256="$(awk '{print $1}' "$AAB.sha256")"
cat >"$MANIFEST" <<JSON
{
  "versionCode": ${VERSION_CODE},
  "versionName": "${VERSION}",
  "channel": "${UPDATE_CHANNEL}",
  "applicationId": "org.jpfchang.clambhook",
  "minSdk": 31,
  "targetSdk": 36,
  "architecture": "arm64-v8a",
  "publishedAt": "${PUBLISHED_AT}",
  "apkUrl": "${RELEASE_BASE}/$(basename "$APK")",
  "apkSha256": "${APK_SHA256}",
  "bundleUrl": "${RELEASE_BASE}/$(basename "$AAB")",
  "bundleSha256": "${AAB_SHA256}",
  "notes": ""
}
JSON
ch_gpg_sign "$MANIFEST"

if ch_gpg_signing_enabled; then
    "$ROOT_DIR/scripts/verify-release-signatures.sh" "$DIST_DIR"
fi

echo "Android release assets written to $DIST_DIR"
