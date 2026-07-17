#!/usr/bin/env bash
set -euo pipefail

echo "internal-only: uploading GNU/Linux release artifacts to Cloudflare R2." >&2

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${1:-$ROOT_DIR/dist/linux}"
BUCKET="${CLAMBHOOK_R2_BUCKET:-clambhook-artifacts}"
VERSION="${VERSION:-$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo 'unknown')}"
UPDATE_CHANNEL="${UPDATE_CHANNEL:-stable}"

if [[ ! -d "$DIST_DIR" ]]; then
    echo "Linux release directory not found: $DIST_DIR" >&2
    echo "Run 'make release-linux' first to build the GNU/Linux packages." >&2
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

ARCH="$(uname -m)"

# Upload versioned artifacts under a versioned prefix (kept for rollback/history).
VERSIONED_PREFIX="clambhook/linux/${VERSION}/${ARCH}"

# Upload latest stable/beta keys at fixed paths so the website routes never
# change when a new release is published.
if [[ "$UPDATE_CHANNEL" == "beta" ]]; then
    LATEST_PREFIX="clambhook/linux/beta"
else
    LATEST_PREFIX="clambhook/linux/stable"
fi

for suffix in deb rpm flatpak AppImage; do
    # Note: AppImage preserves its canonical extension; the others use lowercase.
    case "$suffix" in
        deb|rpm|flatpak) artifact="clambhook-${VERSION}-${ARCH}.${suffix}" ;;
        AppImage) artifact="clambhook-${VERSION}-${ARCH}.AppImage" ;;
    esac
    artifact_path="$DIST_DIR/$artifact"
    if [[ ! -f "$artifact_path" ]]; then
        continue
    fi

    # Map package key used by the website variable naming.
    pkg="$suffix"
    [[ "$pkg" == "AppImage" ]] && pkg="appimage"

    # Binary package.
    case "$suffix" in
        deb) content_type="application/vnd.debian.binary-package" ;;
        rpm) content_type="application/x-rpm" ;;
        flatpak) content_type="application/vnd.flatpak" ;;
        AppImage) content_type="application/x-appimage" ;;
    esac

    upload "$artifact_path" "$VERSIONED_PREFIX/$artifact" "$content_type"
    upload "$artifact_path" "$LATEST_PREFIX/$artifact" "$content_type"

    # SHA-256 checksum.
    if [[ -f "$artifact_path.sha256" ]]; then
        upload "$artifact_path.sha256" "$VERSIONED_PREFIX/$artifact.sha256" "text/plain"
        upload "$artifact_path.sha256" "$LATEST_PREFIX/$artifact.sha256" "text/plain"
    fi

    # Detached GPG signature over the checksum file.
    if [[ -f "$artifact_path.sha256.sig" ]]; then
        upload "$artifact_path.sha256.sig" "$VERSIONED_PREFIX/$artifact.sha256.sig" "application/pgp-signature"
        upload "$artifact_path.sha256.sig" "$LATEST_PREFIX/$artifact.sha256.sig" "application/pgp-signature"
    fi
done

# GNU/Linux update manifest.
MANIFEST="$DIST_DIR/clambhook-linux-manifest.json"
if [[ -f "$MANIFEST" ]]; then
    if [[ "$UPDATE_CHANNEL" == "beta" ]]; then
        MANIFEST_KEY="clambhook/linux/beta/clambhook-linux-manifest.json"
    else
        MANIFEST_KEY="clambhook/linux/stable/clambhook-linux-manifest.json"
    fi
    upload "$MANIFEST" "$VERSIONED_PREFIX/clambhook-linux-manifest.json" "application/json"
    upload "$MANIFEST" "$MANIFEST_KEY" "application/json"

    if [[ -f "$MANIFEST.sig" ]]; then
        upload "$MANIFEST.sig" "$VERSIONED_PREFIX/clambhook-linux-manifest.json.sig" "application/pgp-signature"
        upload "$MANIFEST.sig" "$MANIFEST_KEY.sig" "application/pgp-signature"
    fi
fi

echo "R2 upload complete for GNU/Linux ${VERSION} (${ARCH}) on ${UPDATE_CHANNEL} channel."
