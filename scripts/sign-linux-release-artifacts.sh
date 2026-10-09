#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Sign Ubuntu and Fedora packages after isolated container builds. The private
# key remains on the protected runner, never in a container. For every package:
#   1. embed an OpenPGP signature (rpmsign / debsigs) and verify it,
#   2. regenerate its checksum (embedding changes the package bytes),
#   3. write detached .sig files for the package and its checksum.
# The update manifest is then regenerated from the signed packages and signed.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${1:-$ROOT_DIR/dist/linux}"
# shellcheck source=scripts/lib/release-gpg.sh
source "$ROOT_DIR/scripts/lib/release-gpg.sh"
# shellcheck source=scripts/lib/linux-package-signing.sh
source "$ROOT_DIR/scripts/lib/linux-package-signing.sh"

[[ -d "$DIST_DIR" ]] || ch_gpg_fail "Linux release directory not found: $DIST_DIR"
ch_gpg_signing_enabled || ch_gpg_fail "Linux release signing requires REQUIRE_SIGNING=1"
ch_gpg_require gpg "Linux release signing"
ch_gpg_check_expiry

shopt -s nullglob
rpms=("$DIST_DIR"/*.rpm)
debs=("$DIST_DIR"/*.deb)
(( ${#rpms[@]} + ${#debs[@]} > 0 )) || ch_gpg_fail "no Linux packages found in $DIST_DIR"

for package in "${rpms[@]}"; do
    ch_rpm_sign "$package"
    ch_rpm_verify "$package"
    ch_checksum_and_sign "$package"
done

for package in "${debs[@]}"; do
    ch_deb_sign "$package"
    ch_deb_verify "$package"
    ch_checksum_and_sign "$package"
done

rm -f "$DIST_DIR"/clambhook-linux-*-manifest.json "$DIST_DIR"/clambhook-linux-*-manifest.json.sig
CLAMBHOOK_LINUX_DIST_DIR="$DIST_DIR" REQUIRE_SIGNING=1 \
    "$ROOT_DIR/scripts/release-linux.sh" manifest
