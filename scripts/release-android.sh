#!/usr/bin/env bash
# Build, checksum, and GPG-sign the ClambHook Android release APK, generate the
# update manifest, and print the Cloudflare R2 keys and clambercloud.com env
# vars to publish. Run from the repository root:
#
#   UPDATE_CHANNEL=stable REQUIRE_SIGNING=1 GPG_KEY=EAA876B70B1832F5 \
#     scripts/release-android.sh
#
# Produces the APK, a .sha256 checksum, a detached armored .sha256.sig, and
# clambhook-android-manifest.json (+ .sig). Never publish these artifacts on
# GitHub Releases — upload only to the store.clambercloud.com R2 bucket and
# set the CLAMBHOOK_STABLE_APK_* / CLAMBHOOK_STABLE_ANDROID_MANIFEST_URL
# variables on the Pages project.
set -euo pipefail

echo "internal-only: building Android release artifacts for store.clambercloud.com." >&2

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

"$ROOT_DIR/scripts/check-source-only.sh" "$ROOT_DIR"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)}"
UPDATE_CHANNEL="${UPDATE_CHANNEL:-stable}"
CHAN="$(echo "$UPDATE_CHANNEL" | tr '[:lower:]' '[:upper:]')"
REQUIRE_SIGNING="${REQUIRE_SIGNING:-1}"
GPG_KEY="${GPG_KEY:-EAA876B70B1832F5}"
DIST_DIR="$ROOT_DIR/dist/android"
BUCKET="${CLAMBHOOK_R2_BUCKET:-clambhook-artifacts}"
ANDROID_HOME="${ANDROID_HOME:-$(uname -s | grep -qi darwin && echo "$HOME/Library/Android/sdk" || echo "$ANDROID_HOME")}"

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

require() { command -v "$1" >/dev/null 2>&1 || { echo "$1 is required for $2." >&2; exit 2; }; }

gpg_sign() {
  local target="$1"
  if [[ "$REQUIRE_SIGNING" != "1" ]]; then
    echo "REQUIRE_SIGNING!=1: skipping signature for $target" >&2
    return 0
  fi
  require gpg "release signing"
  gpg --batch --yes --pinentry-mode loopback --local-user "$GPG_KEY" \
    --detach-sign --armor --output "$target.sig" "$target"
  echo "GPG-signed $target → $target.sig"
}

checksum_and_sign() {
  # checksum_and_sign <artifact-path>
  local artifact="$1"
  local name
  name="$(basename "$artifact")"
  ( cd "$(dirname "$artifact")" && sha256sum "$name" > "$name.sha256" )
  gpg_sign "$artifact.sha256"
  echo "  sha256: $(awk '{print $1}' "$artifact.sha256")"
}

# 1. Build the embedded daemon AAR (gomobile bind).
echo "== Building AAR =="
make build-android-mobile-aar

# 2. Build the release APK. The Gradle project produces a single universal APK
#    (no ABI splits). If ui/android/keystore.properties exists the APK is
#    signed at build time; otherwise it is unsigned (sideloadable with a
#    security warning).
echo "== Building release APK =="
( cd "$ROOT_DIR/ui/android" && ANDROID_HOME="$ANDROID_HOME" ./gradlew :app:assembleRelease )

APK_SRC="$ROOT_DIR/ui/android/app/build/outputs/apk/release/app-release.apk"
if [[ ! -f "$APK_SRC" ]]; then
    APK_SRC="$ROOT_DIR/ui/android/app/build/outputs/apk/release/app-release-unsigned.apk"
fi
APK_NAME="ClambHook-${VERSION}.apk"
APK="$DIST_DIR/$APK_NAME"
cp "$APK_SRC" "$APK"
checksum_and_sign "$APK"

# 3. Generate the Android update manifest. The website serves this at
#    /api/clambhook/android-manifest so the in-app UpdateManager can detect
#    newer signed APKs from clambercloud.com. The apkUrl points at the
#    clambercloud.com download route (same host as MANIFEST_URL in
#    UpdateManager.kt).
MANIFEST="$DIST_DIR/clambhook-android-manifest.json"
PUBLISHED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
SHA256="$(awk '{print $1}' "$APK.sha256")"

# versionCode and minSdk must match build.gradle.kts defaultConfig.
VERSION_CODE=2
MIN_SDK=30

{
  printf '{\n'
  printf '  "versionCode": %s,\n' "$VERSION_CODE"
  printf '  "versionName": "%s",\n' "$VERSION"
  printf '  "minSdk": %s,\n' "$MIN_SDK"
  printf '  "publishedAt": "%s",\n' "$PUBLISHED_AT"
  printf '  "apkUrl": "https://clambercloud.com/api/clambhook/download?platform=android",\n'
  printf '  "sha256": "%s",\n' "$SHA256"
  printf '  "notes": ""\n'
  printf '}\n'
} > "$MANIFEST"

gpg_sign "$MANIFEST"

echo "Generated $MANIFEST"

cat <<SUMMARY

Android release artifacts written to $DIST_DIR
Upload each to r2://$BUCKET/clambhook/android/ and set these Pages variables:
  CLAMBHOOK_${CHAN}_APK_URL               → ClambHook-${VERSION}.apk
  CLAMBHOOK_${CHAN}_APK_SHA256_URL        → ClambHook-${VERSION}.apk.sha256
  CLAMBHOOK_${CHAN}_APK_SHA256_SIG_URL    → ClambHook-${VERSION}.apk.sha256.sig
  CLAMBHOOK_${CHAN}_ANDROID_MANIFEST_URL  → clambhook-android-manifest.json
  CLAMBHOOK_${CHAN}_ANDROID_MANIFEST_SIG_URL → clambhook-android-manifest.json.sig
Do not publish these on GitHub Releases or package mirrors.
SUMMARY