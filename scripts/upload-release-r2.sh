#!/usr/bin/env bash
set -euo pipefail

echo "internal-only: uploading notarized macOS installer to Cloudflare R2." >&2

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FINAL_ZIP="${1:-$ROOT_DIR/dist/macos/ClambhookMac-arm64.zip}"
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
VERSIONED_KEY="ClambhookMac-arm64-${VERSION}.zip"
echo "Uploading → r2://$BUCKET/$VERSIONED_KEY"
wrangler r2 object put "$BUCKET/$VERSIONED_KEY" \
    --file "$FINAL_ZIP" \
    --content-type "application/zip"

# Overwrite the latest key (what /api/clambhook/download serves by default).
LATEST_KEY="ClambhookMac-arm64.zip"
echo "Uploading → r2://$BUCKET/$LATEST_KEY"
wrangler r2 object put "$BUCKET/$LATEST_KEY" \
    --file "$FINAL_ZIP" \
    --content-type "application/zip"

echo "R2 upload complete: $VERSIONED_KEY and $LATEST_KEY"
