#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Release gate: every installer, checksum, manifest, and repository bundle must
# carry a valid detached signature from the pinned developer@jpfchang.org key.
# Packages are also checked for their embedded signatures, and checksum files
# must match the artifact they describe. Run immediately before upload.
#
#   scripts/verify-release-signatures.sh dist/linux
#   scripts/verify-release-signatures.sh dist/macos/ClambhookMac-arm64.dmg ...
#
# A directory argument is scanned (depth 1) for *.deb, *.rpm, *.apk, *.aab,
# *.dmg, *.sha256, *-manifest.json, and *.tar.gz; file arguments are verified
# as given. Verification uses only keys/clambhook-release-key.asc.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/release-gpg.sh
source "$ROOT_DIR/scripts/lib/release-gpg.sh"
# shellcheck source=scripts/lib/linux-package-signing.sh
source "$ROOT_DIR/scripts/lib/linux-package-signing.sh"

(( $# > 0 )) || ch_gpg_fail "usage: $0 <dist-dir|file>..." 2
ch_gpg_check_expiry

targets=()
for argument in "$@"; do
    if [[ -d "$argument" ]]; then
        while IFS= read -r -d '' found; do
            targets+=("$found")
        done < <(find "$argument" -maxdepth 1 -type f \( \
            -name '*.deb' -o -name '*.rpm' -o -name '*.apk' -o -name '*.aab' \
            -o -name '*.dmg' -o -name '*.sha256' -o -name '*-manifest.json' \
            -o -name '*.tar.gz' \) -print0 | sort -z)
    elif [[ -f "$argument" ]]; then
        targets+=("$argument")
    else
        ch_gpg_fail "release asset not found: $argument"
    fi
done
(( ${#targets[@]} > 0 )) || ch_gpg_fail "no release assets to verify in: $*"

verify_checksum_file() {
    local checksum="$1" dir expected name actual
    dir="$(dirname "$checksum")"
    read -r expected name <"$checksum"
    name="${name#\*}"
    [[ -n "$expected" && -n "$name" ]] || ch_gpg_fail "malformed checksum file: $checksum"
    [[ "$name" == "$(basename "$name")" ]] || ch_gpg_fail "checksum names a path: $checksum"
    [[ -f "$dir/$name" ]] || ch_gpg_fail "$checksum describes missing artifact $name"
    if command -v sha256sum >/dev/null 2>&1; then
        actual="$(sha256sum "$dir/$name" | awk '{print $1}')"
    else
        actual="$(shasum -a 256 "$dir/$name" | awk '{print $1}')"
    fi
    [[ "$actual" == "$expected" ]] || ch_gpg_fail "checksum mismatch for $dir/$name"
}

for target in "${targets[@]}"; do
    ch_gpg_verify "$target"
    case "$target" in
        *.sha256) verify_checksum_file "$target" ;;
        *.rpm) ch_rpm_verify "$target" ;;
        *.deb) ch_deb_verify "$target" ;;
    esac
done

echo "All ${#targets[@]} release assets carry valid developer@jpfchang.org signatures."
