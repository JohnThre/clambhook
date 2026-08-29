#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Build, checksum, and GPG-sign the ClambHook Android APK and generate its
# GitHub-hosted update manifest. Run from the repository root:
#
#   UPDATE_CHANNEL=stable REQUIRE_SIGNING=1 GPG_KEY=EAA876B70B1832F5 \
#     scripts/release-android.sh
#
# Produces the APK, a .sha256 checksum, a detached armored .sha256.sig, and
# clambhook-android-manifest.json (+ .sig).
set -euo pipefail

echo "Building Android release assets for GitHub Releases." >&2

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

"$ROOT_DIR/scripts/check-source-only.sh" "$ROOT_DIR"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)}"
VERSION_CODE="${VERSION_CODE:-3}"
UPDATE_CHANNEL="${UPDATE_CHANNEL:-stable}"
REQUIRE_SIGNING="${REQUIRE_SIGNING:-1}"
GPG_KEY="${GPG_KEY:-EAA876B70B1832F5}"
DIST_DIR="$ROOT_DIR/dist/android"
RELEASE_TAG="${RELEASE_TAG:-v${VERSION}}"
RELEASE_BASE="https://github.com/${GITHUB_REPOSITORY:-JohnThre/clambhook}/releases/download/${RELEASE_TAG}"
ANDROID_HOME="${ANDROID_HOME:-$(uname -s | grep -qi darwin && echo "$HOME/Library/Android/sdk" || echo "$ANDROID_HOME")}"

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

require() { command -v "$1" >/dev/null 2>&1 || {
  echo "$1 is required for $2." >&2
  exit 2
}; }

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
    "${passphrase_args[@]}" \
    --detach-sign --armor --output "$target.sig" "$target"
  echo "GPG-signed $target → $target.sig"
}

checksum_and_sign() {
  # checksum_and_sign <artifact-path>
  local artifact="$1"
  local name
  name="$(basename "$artifact")"
  (cd "$(dirname "$artifact")" && sha256sum "$name" >"$name.sha256")
  gpg_sign "$artifact.sha256"
  echo "  sha256: $(awk '{print $1}' "$artifact.sha256")"
}

# 1. Build the native C/JNI release APK. Protected CI performs this build
# without signing credentials, signs the completed APK with
# the trusted Android SDK apksigner, and then calls this script with
# CLAMBHOOK_ANDROID_SKIP_BUILD=1 to package its metadata.
if [[ "${CLAMBHOOK_ANDROID_SKIP_BUILD:-0}" != "1" ]]; then
  # Day-to-day Android development uses Google's `android` CLI by default (see
  # docs/android-development.md). Release assembly stays on Gradle because the
  # `android` CLI has no release-build command.
  echo "== Building release APK =="
  (cd "$ROOT_DIR/ui/android" && ANDROID_HOME="$ANDROID_HOME" ./gradlew \
    :app:assembleRelease \
    -Pclambhook.versionName="$VERSION" \
    -Pclambhook.versionCode="$VERSION_CODE")
else
  echo "== Using prebuilt, apksigner-verified release APK =="
fi

APK_SRC="$ROOT_DIR/ui/android/app/build/outputs/apk/release/app-release.apk"
if [[ ! -f "$APK_SRC" ]]; then
  APK_SRC="$ROOT_DIR/ui/android/app/build/outputs/apk/release/app-release-unsigned.apk"
fi
if [[ "${CLAMBHOOK_ANDROID_SKIP_BUILD:-0}" == "1" && "$APK_SRC" == *-unsigned.apk ]]; then
  echo "Protected CI requires the apksigner-verified release APK." >&2
  exit 1
fi
[[ -f "$APK_SRC" ]] || {
  echo "Android release APK not found: $APK_SRC" >&2
  exit 1
}
APK_NAME="ClambHook.apk"
APK="$DIST_DIR/$APK_NAME"
cp "$APK_SRC" "$APK"
checksum_and_sign "$APK"

# 3. Generate the update manifest with an immutable versioned asset URL.
MANIFEST="$DIST_DIR/clambhook-android-manifest.json"
PUBLISHED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
SHA256="$(awk '{print $1}' "$APK.sha256")"

# minSdk must match build.gradle.kts defaultConfig. VERSION_CODE is supplied by
# protected CI (or defaults to the current checked-in value for local release).
MIN_SDK=31

{
  printf '{\n'
  printf '  "versionCode": %s,\n' "$VERSION_CODE"
  printf '  "versionName": "%s",\n' "$VERSION"
  printf '  "minSdk": %s,\n' "$MIN_SDK"
  printf '  "publishedAt": "%s",\n' "$PUBLISHED_AT"
  printf '  "apkUrl": "%s/%s",\n' "$RELEASE_BASE" "$APK_NAME"
  printf '  "sha256": "%s",\n' "$SHA256"
  printf '  "notes": ""\n'
  printf '}\n'
} >"$MANIFEST"

gpg_sign "$MANIFEST"

echo "Generated $MANIFEST"

cat <<SUMMARY

Android release artifacts written to $DIST_DIR
Publish these files on GitHub Release $RELEASE_TAG.
SUMMARY
