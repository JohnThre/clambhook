#!/usr/bin/env bash
set -euo pipefail

echo "internal-only: uploading notarized macOS installer to Cloudflare R2." >&2

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FINAL_ZIP="${1:-$ROOT_DIR/dist/macos/ClambhookMac-arm64.zip}"
FINAL_DMG="${2:-$ROOT_DIR/dist/macos/ClambhookMac-arm64.dmg}"
FINAL_DMG_CHECKSUM="${3:-$ROOT_DIR/dist/macos/ClambhookMac-arm64.dmg.sha256}"
UPDATE_MANIFEST="${4:-$ROOT_DIR/dist/macos/clambhook-update-manifest.json}"
BUCKET="${CLAMBHOOK_R2_BUCKET:-clambhook-artifacts}"
VERSION="${VERSION:-$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo 'unknown')}"

if [[ ! -f "$FINAL_ZIP" ]]; then
    echo "Installer not found: $FINAL_ZIP" >&2
    echo "Run 'make release-macos' first to build and notarize the installer." >&2
    exit 1
fi

if ! command -v wrangler >/dev/null 2>&1; then
    echo "wrangler is required for R2 upload. Install it:" >&2
    echo "  npm install -g wrangler" >&2
    echo "  wrangler login" >&2
    exit 1
fi

# Upload versioned copy (kept for rollback).
VERSIONED_KEY="*******************${VERSION}.zip"
echo "Uploading → r2://$BUCKET/$VERSIONED_KEY"
wrangler r2 object put "$BUCKET/$VERSIONED_KEY" \
    --file "$FINAL_ZIP" \
    --content-type "application/zip" \
    --remote

# Overwrite the latest ZIP key (backward compat).
LATEST_KEY="**********************"
echo "Uploading → r2://$BUCKET/$LATEST_KEY"
wrangler r2 object put "$BUCKET/$LATEST_KEY" \
    --file "$FINAL_ZIP" \
    --content-type "application/zip" \
    --remote

# Upload DMG artifacts when present.
if [[ -f "$FINAL_DMG" ]]; then
    DMG_VERSIONED_KEY="macos/clambhook-mac-${VERSION}.dmg"
    echo "Uploading → r2://$BUCKET/$DMG_VERSIONED_KEY"
    wrangler r2 object put "$BUCKET/$DMG_VERSIONED_KEY" \
        --file "$FINAL_DMG" \
        --content-type "application/x-apple-diskimage" \
        --remote

    DMG_LATEST_KEY="ClambhookMac-arm64.dmg"
    echo "Uploading → r2://$BUCKET/$DMG_LATEST_KEY"
    wrangler r2 object put "$BUCKET/$DMG_LATEST_KEY" \
        --file "$FINAL_DMG" \
        --content-type "application/x-apple-diskimage" \
        --remote
fi

if [[ -f "$FINAL_DMG_CHECKSUM" ]]; then
    CHECKSUM_KEY="ClambhookMac-arm64.dmg.sha256"
    echo "Uploading → r2://$BUCKET/$CHECKSUM_KEY"
    wrangler r2 object put "$BUCKET/$CHECKSUM_KEY" \
        --file "$FINAL_DMG_CHECKSUM" \
        --content-type "text/plain" \
        --remote
fi

if [[ -f "$UPDATE_MANIFEST" ]]; then
    MANIFEST_KEY="clambhook-update-manifest.json"
    echo "Uploading → r2://$BUCKET/$MANIFEST_KEY"
    wrangler r2 object put "$BUCKET/$MANIFEST_KEY" \
        --file "$UPDATE_MANIFEST" \
        --content-type "application/json" \
        --remote
fi

echo "R2 upload complete: $VERSIONED_KEY, $LATEST_KEY, DMG, checksum, and update manifest"
