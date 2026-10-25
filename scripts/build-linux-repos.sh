#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Build signed apt (Ubuntu) and dnf (Fedora) repositories from already-signed
# release packages, and bundle them for deployment to
# https://jpfchang.org/clambhook/linux/. Every package is verified before it is
# indexed; repository metadata is signed with the developer@jpfchang.org key.
#
#   UPDATE_CHANNEL=stable VERSION=1.2.3 \
#     scripts/build-linux-repos.sh <signed-packages-dir> <output-dir> [more-package-dirs...]
#
# Pass the core package directories and the clambhook-ui package directories
# from the apps repository; one repository serves both packages.
#
# CLAMBHOOK_REPO_STAGE splits the work for a maintainer machine without the
# Debian/Fedora tooling:
#   generate  verify every package and write the unsigned repository tree
#             (run in a key-free Ubuntu container with apt-utils,
#             createrepo-c, rpm, debsig-verify, and gpg);
#   sign      sign and verify the repository metadata and bundle it (run on
#             the host that holds the signing key);
#   all       both (default; a GNU/Linux host with every tool and the key).
#
# Layout of <output-dir>/clambhook-linux-repo/:
#   clambhook-release-key.asc
#   apt/pool/main/c/clambhook/*.deb
#   apt/dists/<channel>/{Release,InRelease,Release.gpg}
#   apt/dists/<channel>/main/binary-{amd64,arm64}/Packages{,.gz}
#   rpm/<channel>/{x86_64,aarch64}/*.rpm + repodata/repomd.xml{,.asc}
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/release-gpg.sh
source "$ROOT_DIR/scripts/lib/release-gpg.sh"
# shellcheck source=scripts/lib/linux-package-signing.sh
source "$ROOT_DIR/scripts/lib/linux-package-signing.sh"

INPUT_DIRS=("${1:?signed package directory is required}")
OUTPUT_DIR="${2:?output directory is required}"
INPUT_DIRS+=("${@:3}")
VERSION="${VERSION:?VERSION is required}"
CHANNEL="${UPDATE_CHANNEL:-stable}"
[[ "$CHANNEL" == "stable" || "$CHANNEL" == "beta" ]] ||
    ch_gpg_fail "UPDATE_CHANNEL must be stable or beta" 2

STAGE="${CLAMBHOOK_REPO_STAGE:-all}"
[[ "$STAGE" == "generate" || "$STAGE" == "sign" || "$STAGE" == "all" ]] ||
    ch_gpg_fail "CLAMBHOOK_REPO_STAGE must be generate, sign, or all" 2
REPO="$OUTPUT_DIR/clambhook-linux-repo"

sign_repository() {
    ch_gpg_signing_enabled || ch_gpg_fail "repository signing requires REQUIRE_SIGNING=1"
    ch_gpg_require tar "repository bundling"
    ch_gpg_check_expiry
    local dists="$REPO/apt/dists/$CHANNEL" arch_dir allowlist package
    [[ -f "$dists/Release" ]] || ch_gpg_fail "unsigned repository not found in $REPO"
    # The tree may come from a container: sign it only if every package is one
    # this host verified and every metadata hash matches the files it names.
    allowlist="$(mktemp "${TMPDIR:-/tmp}/clambhook-repo-allow.XXXXXX")"
    shopt -s nullglob
    for input_dir in "${INPUT_DIRS[@]}"; do
        for package in "$input_dir"/*.deb "$input_dir"/*.rpm; do
            ch_gpg_verify "$package"
            case "$package" in
                *.deb) ch_deb_verify "$package" ;;
                *.rpm) ch_rpm_verify "$package" ;;
            esac
            if command -v sha256sum >/dev/null 2>&1; then
                sha256sum "$package" | awk '{print $1}' >>"$allowlist"
            else
                shasum -a 256 "$package" | awk '{print $1}' >>"$allowlist"
            fi
        done
    done
    python3 "$ROOT_DIR/scripts/lib/verify_repo_tree.py" "$REPO" "$CHANNEL" "$allowlist" || {
        rm -f "$allowlist"
        ch_gpg_fail "refusing to sign an unverified repository tree"
    }
    rm -f "$allowlist"
    ch_gpg_clearsign "$dists/Release" "$dists/InRelease"
    ch_gpg_sign "$dists/Release" "$dists/Release.gpg"
    ch_gpg_verify_clearsigned "$dists/InRelease"
    ch_gpg_verify "$dists/Release" "$dists/Release.gpg"
    for arch_dir in "$REPO/rpm/$CHANNEL"/*/; do
        arch_dir="${arch_dir%/}"
        ch_gpg_sign "$arch_dir/repodata/repomd.xml" "$arch_dir/repodata/repomd.xml.asc"
        ch_gpg_verify "$arch_dir/repodata/repomd.xml" "$arch_dir/repodata/repomd.xml.asc"
    done
    local bundle="$OUTPUT_DIR/clambhook-linux-repo-${VERSION}.tar.gz"
    local -a tar_flags=(--owner=0 --group=0 --numeric-owner)
    tar --version 2>/dev/null | grep -q 'GNU tar' && tar_flags+=(--sort=name)
    tar -C "$OUTPUT_DIR" "${tar_flags[@]}" -czf "$bundle" clambhook-linux-repo
    ch_checksum_and_sign "$bundle"
    echo "Signed apt/dnf repositories written to $REPO and bundled as $bundle"
}

if [[ "$STAGE" == "sign" ]]; then
    sign_repository
    exit 0
fi

for tool in apt-ftparchive createrepo_c dpkg-deb rpm gzip tar; do
    ch_gpg_require "$tool" "Linux repository generation"
done

rm -rf "$REPO"
mkdir -p "$REPO"
install -m 0644 "$CLAMBHOOK_RELEASE_PUBLIC_KEY" "$REPO/clambhook-release-key.asc"

shopt -s nullglob
debs=()
rpms=()
for input_dir in "${INPUT_DIRS[@]}"; do
    [[ -d "$input_dir" ]] || ch_gpg_fail "package directory not found: $input_dir"
    debs+=("$input_dir"/*.deb)
    rpms+=("$input_dir"/*.rpm)
done
(( ${#debs[@]} > 0 )) || ch_gpg_fail "no .deb packages in ${INPUT_DIRS[*]}"
(( ${#rpms[@]} > 0 )) || ch_gpg_fail "no .rpm packages in ${INPUT_DIRS[*]}"

# --- apt ---------------------------------------------------------------------
apt_root="$REPO/apt"
pool="$apt_root/pool/main/c/clambhook"
mkdir -p "$pool"
deb_arches=()
for deb in "${debs[@]}"; do
    ch_gpg_verify "$deb"
    ch_deb_verify "$deb"
    install -m 0644 "$deb" "$pool/"
    arch="$(dpkg-deb -f "$deb" Architecture)"
    [[ " ${deb_arches[*]} " == *" $arch "* ]] || deb_arches+=("$arch")
done

dists="$apt_root/dists/$CHANNEL"
for arch in "${deb_arches[@]}"; do
    binary="$dists/main/binary-$arch"
    mkdir -p "$binary"
    (cd "$apt_root" && apt-ftparchive --arch "$arch" packages pool) >"$binary/Packages"
    gzip -9nkf "$binary/Packages"
done

(cd "$apt_root" && apt-ftparchive \
    -o "APT::FTPArchive::Release::Origin=ClambHook" \
    -o "APT::FTPArchive::Release::Label=ClambHook" \
    -o "APT::FTPArchive::Release::Suite=$CHANNEL" \
    -o "APT::FTPArchive::Release::Codename=$CHANNEL" \
    -o "APT::FTPArchive::Release::Version=$VERSION" \
    -o "APT::FTPArchive::Release::Architectures=${deb_arches[*]}" \
    -o "APT::FTPArchive::Release::Components=main" \
    -o "APT::FTPArchive::Release::Description=ClambHook packages for Ubuntu" \
    release "dists/$CHANNEL") >"$dists/Release.tmp"
mv "$dists/Release.tmp" "$dists/Release"

# --- dnf ---------------------------------------------------------------------
rpm_root="$REPO/rpm/$CHANNEL"
for package in "${rpms[@]}"; do
    ch_gpg_verify "$package"
    ch_rpm_verify "$package"
    arch="$(rpm -qp --qf '%{ARCH}' "$package" 2>/dev/null)"
    [[ "$arch" == "x86_64" || "$arch" == "aarch64" ]] ||
        ch_gpg_fail "unsupported RPM architecture $arch in $package"
    mkdir -p "$rpm_root/$arch"
    install -m 0644 "$package" "$rpm_root/$arch/"
done
for arch_dir in "$rpm_root"/*/; do
    arch_dir="${arch_dir%/}"
    createrepo_c --quiet --general-compress-type=gz "$arch_dir"
done

if [[ "$STAGE" == "generate" ]]; then
    echo "Unsigned apt/dnf repositories written to $REPO"
    exit 0
fi
sign_repository
