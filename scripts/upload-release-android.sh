#!/usr/bin/env bash
set -euo pipefail

echo "internal-only: uploading Android release artifacts to Cloudflare R2." >&2

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${1:-$ROOT_DIR/dist/android}"
BUCKET="${CLAMBHOOK_R2_BUCKET:-clambhook-artifacts}"
VERSION="${VERSION:-$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo 'unknown')}"
UPDATE_CHANNEL="${UPDATE_CHANNEL:-stable}"

if [[ ! -d "$DIST_DIR" ]]; then
    echo "Android release directory not found: $DIST_DIR" >&2
    echo "Run 'make release-android' first to build the APK and manifest." >&2
    exit 1
fi

if ! command -v wrangler >/dev/null 2>&1; then
    echo "wrangler is required for R2 upload. Install it:" >&2
    echo "  npm install -g wrangler" >&2
    echo "  wrangler login" >&2
    exit 1
fi

upload() {
    local src="$1"
    local key="$2"
    local content_type="$3"
    if [[ ! -f "$src" ]]; then
        echo "Skipping missing artifact: $src" >&2
        return 0
    fi
    echo "Uploading → r2://$BUCKET/$key"
    wrangler r2 object put "$BUCKET/$key" \
        --file "$src" \
        --content-type "$content_type" \
        --remote
}

# Upload versioned artifacts under a versioned prefix (kept for rollback/history).
VERSIONED_PREFIX="clambhook/android/${VERSION}"

# Upload latest stable/beta keys at fixed paths so the website routes never
# change when a new release is published.
if [[ "$UPDATE_CHANNEL" == "beta" ]]; then
    LATEST_PREFIX="clambhook/android/beta"
else
    LATEST_PREFIX="clambhook/android/stable"
fi

APK_NAME="ClambHook-${VERSION}.apk"
APK="$DIST_DIR/$APK_NAME"

# APK binary.
APK_CONTENT_TYPE="application/vnd.android.package-archive"
upload "$APK" "$VERSIONED_PREFIX/$APK_NAME" "$APK_CONTENT_TYPE"
upload "$APK" "$LATEST_PREFIX/ClambHook.apk" "$APK_CONTENT_TYPE"

# SHA-256 checksum.
if [[ -f "$APK.sha256" ]]; then
    upload "$APK.sha256" "$VERSIONED_PREFIX/$APK_NAME.sha256" "text/plain"
    upload "$APK.sha256" "$LATEST_PREFIX/ClambHook.apk.sha256" "text/plain"
fi

# Detached GPG signature over the checksum file.
if [[ -f "$APK.sha256.sig" ]]; then
    upload "$APK.sha256.sig" "$VERSIONED_PREFIX/$APK_NAME.sha256.sig" "application/pgp-signature"
    upload "$APK.sha256.sig" "$LATEST_PREFIX/ClambHook.apk.sha256.sig" "application/pgp-signature"
fi

# Android update manifest.
MANIFEST="$DIST_DIR/clambhook-android-manifest.json"
if [[ -f "$MANIFEST" ]]; then
    MANIFEST_KEY="$LATEST_PREFIX/clambhook-android-manifest.json"
    upload "$MANIFEST" "$VERSIONED_PREFIX/clambhook-android-manifest.json" "application/json"
    upload "$MANIFEST" "$MANIFEST_KEY" "application/json"

    if [[ -f "$MANIFEST.sig" ]]; then
        upload "$MANIFEST.sig" "$VERSIONED_PREFIX/clambhook-android-manifest.json.sig" "application/pgp-signature"
        upload "$MANIFEST.sig" "$MANIFEST_KEY.sig" "application/pgp-signature"
    fi
fi

echo "R2 upload complete for Android ${VERSION} on ${UPDATE_CHANNEL} channel."
echo ""
echo "Set these Cloudflare Pages env vars on the clambercloud Pages project:"
echo "  CLAMBHOOK_STABLE_APK_URL              → r2://$BUCKET/$LATEST_PREFIX/ClambHook.apk"
echo "  CLAMBHOOK_STABLE_APK_SHA256_URL       → r2://$BUCKET/$LATEST_PREFIX/ClambHook.apk.sha256"
echo "  CLAMBHOOK_STABLE_APK_SHA256_SIG_URL   → r2://$BUCKET/$LATEST_PREFIX/ClambHook.apk.sha256.sig"
echo "  CLAMBHOOK_STABLE_ANDROID_MANIFEST_URL → r2://$BUCKET/$LATEST_PREFIX/clambhook-android-manifest.json"
echo "  CLAMBHOOK_STABLE_ANDROID_MANIFEST_SIG_URL → r2://$BUCKET/$LATEST_PREFIX/clambhook-android-manifest.json.sig"