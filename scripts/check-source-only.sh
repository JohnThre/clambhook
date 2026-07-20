#!/usr/bin/env bash
# Enforce the repository's source-only GitHub policy. Installer artifacts are
# built for validation and private distribution, never published by workflows.
set -euo pipefail

ROOT_DIR="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
WORKFLOW_ROOT="$ROOT_DIR/.github"

fail() {
    echo "$1" >&2
    exit 1
}

reject_tree_text() {
    local pattern="$1"
    if [[ -d "$WORKFLOW_ROOT" ]] && grep -RFiq -- "$pattern" "$WORKFLOW_ROOT"; then
        fail "GitHub workflow release policy contains prohibited text: $pattern"
    fi
}

command -v grep >/dev/null 2>&1 || fail "grep is required for source-only policy checks."

reject_tree_text "upload-artifact"
reject_tree_text "gh release upload"
reject_tree_text "softprops/action-gh-release"
reject_tree_text ".dmg"
reject_tree_text ".pkg"
reject_tree_text ".deb"
reject_tree_text ".rpm"
reject_tree_text ".flatpak"
reject_tree_text ".AppImage"

echo "Source-only GitHub policy check passed."
