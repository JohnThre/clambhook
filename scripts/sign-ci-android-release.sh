#!/usr/bin/env bash
# Sign an already-built Android release with apksigner. The keystore exists only
# in the runner temporary directory and is removed before this step exits.
set -euo pipefail

TEMP_ROOT="${1:?temporary root is required}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_TOOLS_VERSION="${ANDROID_BUILD_TOOLS_VERSION:-36.0.0}"
APKSIGNER="${ANDROID_HOME:?ANDROID_HOME is required}/build-tools/$BUILD_TOOLS_VERSION/apksigner"
UNSIGNED_APK="$ROOT_DIR/ui/android/app/build/outputs/apk/release/app-release-unsigned.apk"
SIGNED_APK="$ROOT_DIR/ui/android/app/build/outputs/apk/release/app-release.apk"
KEYSTORE="$TEMP_ROOT/clambhook-android-release-keystore"

for name in ANDROID_KEYSTORE_BASE64 ANDROID_KEYSTORE_PASSWORD ANDROID_KEY_ALIAS ANDROID_KEY_PASSWORD; do
  [[ -n "${!name:-}" ]] || {
    echo "$name is required." >&2
    exit 2
  }
done
[[ -x "$APKSIGNER" ]] || {
  echo "apksigner not found: $APKSIGNER" >&2
  exit 2
}
[[ -f "$UNSIGNED_APK" ]] || {
  echo "Unsigned Android release not found: $UNSIGNED_APK" >&2
  exit 1
}

mkdir -p "$TEMP_ROOT"
chmod 0700 "$TEMP_ROOT"

cleanup() {
  rm -f "$KEYSTORE"
}
trap cleanup EXIT

printf '%s' "$ANDROID_KEYSTORE_BASE64" | openssl base64 -d -A > "$KEYSTORE"
chmod 0600 "$KEYSTORE"
rm -f "$SIGNED_APK"
"$APKSIGNER" sign \
  --ks "$KEYSTORE" \
  --ks-key-alias "$ANDROID_KEY_ALIAS" \
  --ks-pass env:ANDROID_KEYSTORE_PASSWORD \
  --key-pass env:ANDROID_KEY_PASSWORD \
  --out "$SIGNED_APK" \
  "$UNSIGNED_APK"
"$APKSIGNER" verify --verbose --print-certs "$SIGNED_APK"

echo "Android release signature verified."
