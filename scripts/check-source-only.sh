#!/usr/bin/env bash
# Enforce the repository's source-only GitHub policy. Installer artifacts are
# built for validation and private distribution, never published by workflows.
# This guard has two layers:
#   1. Workflow-text scan: reject prohibited release/upload patterns in .github/.
#   2. Tree scan: reject committed binary/installer artifacts anywhere in the
#      repo tree by extension, so a renamed workflow step can't sneak one in.
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

# reject_artifact_ext scans the tracked tree (and the working tree) for
# installer/binary file extensions that must never be committed. It uses git
# ls-files when available (tracked files only, honoring .gitignore) and falls
# back to find for non-git checkouts.
reject_artifact_ext() {
    local pattern="$1"
    local label="$2"
    local matches=""
    if command -v git >/dev/null 2>&1 && git -C "$ROOT_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        matches="$(git -C "$ROOT_DIR" ls-files -z --cached --others --exclude-standard | while IFS= read -r -d '' f; do case "$f" in *"$pattern") printf '%s\n' "$f";; esac; done)"
    else
        matches="$(find "$ROOT_DIR" -type f -name "*$pattern" -not -path '*/.git/*' 2>/dev/null || true)"
    fi
    if [[ -n "$matches" ]]; then
        fail "Source-only policy: $label artifacts found in tree:\n$matches"
    fi
}

command -v grep >/dev/null 2>&1 || fail "grep is required for source-only policy checks."

reject_tree_text "upload-artifact"
reject_tree_text "gh release upload"
reject_tree_text "softprops/action-gh-release"

# Workflow-text patterns for file extensions are redundant with the tree scan
# below, but kept for defense in depth: a workflow that *references* a .dmg by
# name (even without upload-artifact) signals an intent to publish.
reject_tree_text ".dmg"
reject_tree_text ".pkg"
reject_tree_text ".deb"
reject_tree_text ".rpm"
reject_tree_text ".flatpak"
reject_tree_text ".AppImage"

# Tree scan: reject committed installer artifacts by extension anywhere in the
# repo, not just in .github/. This catches binaries that bypass the workflow
# text check via renamed steps or manual commits.
reject_artifact_ext ".dmg" "DMG"
reject_artifact_ext ".pkg" "PKG"
reject_artifact_ext ".apk" "APK"
reject_artifact_ext ".aab" "AAB"
reject_artifact_ext ".deb" "Debian"
reject_artifact_ext ".rpm" "RPM"
reject_artifact_ext ".flatpak" "Flatpak"
reject_artifact_ext ".AppImage" "AppImage"

echo "Source-only GitHub policy check passed."
