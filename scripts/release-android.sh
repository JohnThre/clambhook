#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Build, inspect, checksum, and sign the Gluon Android ARM64 application.
# The protected release job supplies both the Android keystore and the GPG key.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

scripts/check-source-only.sh "$ROOT_DIR"
scripts/check-cutover.sh "$ROOT_DIR"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)}"
VERSION_CODE="${VERSION_CODE:-3}"
UPDATE_CHANNEL="${UPDATE_CHANNEL:-stable}"
REQUIRE_SIGNING="${REQUIRE_SIGNING:-1}"
GPG_KEY="${GPG_KEY:-EAA876B70B1832F5}"
DIST_DIR="$ROOT_DIR/dist/android"
RELEASE_TAG="${RELEASE_TAG:-v${VERSION}}"
RELEASE_BASE="https://github.com/${GITHUB_REPOSITORY:-JohnThre/clambhook}/releases/download/${RELEASE_TAG}"
GVM_DIR="$ROOT_DIR/ui/javafx/target/gluonfx/aarch64-android/gvm"

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

require() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "$1 is required for $2." >&2
        exit 2
    }
}

gpg_sign() {
    local target="$1"
    local passphrase_args=()
    if [[ "$REQUIRE_SIGNING" != "1" ]]; then
        echo "REQUIRE_SIGNING!=1: skipping signature for $target" >&2
        return 0
    fi
    require gpg "release signing"
    if [[ -n "${GPG_PASSPHRASE_FILE:-}" ]]; then
        [[ -f "$GPG_PASSPHRASE_FILE" ]] || {
            echo "GPG_PASSPHRASE_FILE does not exist." >&2
            exit 2
        }
        passphrase_args=(--passphrase-file "$GPG_PASSPHRASE_FILE")
    fi
    gpg --batch --yes --pinentry-mode loopback --local-user "$GPG_KEY" \
        "${passphrase_args[@]}" --detach-sign --armor \
        --output "$target.sig" "$target"
}

checksum_and_sign() {
    local artifact="$1" name
    name="$(basename "$artifact")"
    (cd "$(dirname "$artifact")" && sha256sum "$name" >"$name.sha256")
    gpg_sign "$artifact.sha256"
}

if [[ "${CLAMBHOOK_ANDROID_SKIP_BUILD:-0}" != "1" ]]; then
    [[ -n "${GRAALVM_HOME:-}" ]] || {
        echo "GRAALVM_HOME must point to the pinned GraalVM for JDK 17." >&2
        exit 2
    }
    profiles="android"
    if [[ -n "${CLAMBHOOK_ANDROID_KEYSTORE_PATH:-}" ]]; then
        profiles="android,android-signed"
    elif [[ "$REQUIRE_SIGNING" == "1" ]]; then
        echo "Android release signing credentials are required." >&2
        exit 2
    fi
    bash scripts/prepare-gluon-android.sh
    (cd ui/javafx && mvn -B -P"$profiles" \
        -Dclambhook.version.name="$VERSION" \
        -Dclambhook.version.code="$VERSION_CODE" \
        gluonfx:build gluonfx:package)
fi

APK_SRC="$(find "$GVM_DIR" -maxdepth 1 -type f -name '*.apk' -print -quit 2>/dev/null || true)"
AAB_SRC="$(find "$GVM_DIR" -maxdepth 1 -type f -name '*.aab' -print -quit 2>/dev/null || true)"
[[ -f "$APK_SRC" ]] || { echo "Gluon Android APK was not produced in $GVM_DIR" >&2; exit 1; }
[[ -f "$AAB_SRC" ]] || { echo "Gluon Android App Bundle was not produced in $GVM_DIR" >&2; exit 1; }

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
if unzip -Z1 "$APK" | grep -Eiq '(^|/)(compose|gtk|jre|jdk)(/|\.|$)|\.go$'; then
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

checksum_and_sign "$APK"
checksum_and_sign "$AAB"

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
gpg_sign "$MANIFEST"

echo "Android Gluon release assets written to $DIST_DIR"
