#!/usr/bin/env bash
# GPG-sign the release tag for a macOS release using the configured signing key.
# Run after `make release-macos` and after pushing the tag. Requires a usable
# pinentry on the host (the release key has a passphrase).
#
# Usage:
#   ./scripts/sign-release-tag.sh <version-tag>          # sign an existing tag
#   ./scripts/sign-release-tag.sh <version-tag> create   # create + sign an
#                                                       # annotated tag at HEAD
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TAG="${1:-}"
MODE="${2:-sign}"

if [[ -z "$TAG" ]]; then
    echo "Usage: $0 <version-tag> [create]" >&2
    exit 2
fi

GPG_KEY="${CLAMBHOOK_GPG_KEY:-$(git -C "$ROOT_DIR" config user.signingkey 2>/dev/null || true)}"
if [[ -z "$GPG_KEY" ]]; then
    echo "No GPG signing key configured (set CLAMBHOOK_GPG_KEY or git config user.signingkey)." >&2
    exit 1
fi

cd "$ROOT_DIR"

if [[ "$MODE" == "create" ]]; then
    if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
        echo "Tag $TAG already exists; use plain sign mode." >&2
        exit 1
    fi
    git tag -s -u "$GPG_KEY" -m "ClambHook for macOS $TAG" "$TAG"
    echo "Created and GPG-signed tag $TAG with $GPG_KEY."
else
    if ! git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
        echo "Tag $TAG does not exist. Run with 'create' mode first." >&2
        exit 1
    fi
    # Re-sign an existing tag in place.
    git tag -f -s -u "$GPG_KEY" -m "ClambHook for macOS $TAG" "$TAG"
    echo "GPG-signed existing tag $TAG with $GPG_KEY."
fi

git tag -v "$TAG"
