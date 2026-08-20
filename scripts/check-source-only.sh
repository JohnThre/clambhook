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
        matches="$(git -C "$ROOT_DIR" ls-files -z --cached --others --exclude-standard | while IFS= read -r -d '' f; do case "$f" in *"$pattern") printf '%s\n' "$f" ;; esac done)"
    else
        matches="$(find "$ROOT_DIR" -type f -name "*$pattern" -not -path '*/.git/*' 2>/dev/null || true)"
    fi
    if [[ -n "$matches" ]]; then
        fail "Source-only policy: $label artifacts found in tree:\n$matches"
    fi
}

command -v grep >/dev/null 2>&1 || fail "grep is required for source-only policy checks."

# No GitHub Actions workflows live in this repo: CI/CD runs locally (local
# machine + Apple container; see scripts/ci-local.sh). The text checks below
# remain as a defensive guard against any future workflow that attempts a
# public GitHub Release or a third-party release-upload action.
reject_tree_text "gh release upload"
reject_tree_text "softprops/action-gh-release"
reject_tree_text "actions/create-release"

# The tree scan below is the authoritative guard against committed installer
# artifacts. There is no in-tree workflow text that legitimately references
# package extensions, so any committed .deb/.rpm/.dmg/.apk/etc. is a policy
# violation. Public GitHub Releases remain prohibited via the text checks
# above.

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
