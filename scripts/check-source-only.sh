#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Keep generated installer artifacts out of the source tree. Signed binaries
# are published as GitHub Release assets, never committed to Git.
set -euo pipefail

ROOT_DIR="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"

fail() {
    echo "$1" >&2
    exit 1
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

echo "Source-tree artifact policy check passed."
