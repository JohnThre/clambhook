#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Build, sign, index, and install-test every GNU/Linux release asset on the
# maintainer's machine. Containers (Podman or Docker) build and inspect; they
# never receive the signing key. Signing happens on the host.
#
#   VERSION=1.2.3 UPDATE_CHANNEL=stable scripts/release-linux-local.sh
#
# Steps:
#   1. core .deb/.rpm for x86_64 and aarch64      -> dist/linux-<arch>/
#   2. clambhook-ui .deb/.rpm (apps repository)    -> $APPS/dist/linux-ui-<arch>/
#   3. host: embedded + detached signatures, checksums, manifests
#   4. container: verify every package, generate the unsigned apt/dnf tree
#   5. host: sign the repository metadata and bundle it  -> dist/linux-repo/
#   6. container: install from the signed repositories on Ubuntu and Fedora
#
# CLAMBHOOK_APPS_DIR overrides the apps checkout (default ../clambhook-apps).
# CLAMBHOOK_LINUX_ARCHES limits the architectures (default "x86_64 aarch64").
# CLAMBHOOK_UPGRADE_FROM optionally names a directory with combined 1.0.x
# packages to prove that upgrades keep the desktop controller.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APPS_DIR="${CLAMBHOOK_APPS_DIR:-$ROOT_DIR/../clambhook-apps}"
VERSION="${VERSION:?VERSION is required}"
CHANNEL="${UPDATE_CHANNEL:-stable}"
read -r -a ARCHES <<<"${CLAMBHOOK_LINUX_ARCHES:-x86_64 aarch64}"
cd "$ROOT_DIR"

engine=""
mount_suffix=""
if command -v podman >/dev/null 2>&1; then
    engine="podman"
    mount_suffix=":Z"
elif command -v docker >/dev/null 2>&1; then
    engine="docker"
else
    echo "Podman or Docker is required for the GNU/Linux release." >&2
    exit 2
fi
[[ -x "$APPS_DIR/scripts/release-linux-ui.sh" ]] || {
    echo "clambhook-apps checkout not found at $APPS_DIR" >&2
    exit 2
}

platform_for() {
    case "$1" in
        x86_64) echo linux/amd64 ;;
        aarch64) echo linux/arm64 ;;
        *) echo "unsupported architecture: $1" >&2; exit 2 ;;
    esac
}

core_dirs=()
ui_dirs=()
for arch in "${ARCHES[@]}"; do
    platform="$(platform_for "$arch")"
    echo "== core packages: $arch =="
    CLAMBHOOK_LINUX_RELEASE_BUILD=1 CLAMBHOOK_LINUX_PLATFORM="$platform" \
        CLAMBHOOK_LINUX_DIST_SUBDIR="dist/linux-$arch" VERSION="$VERSION" \
        UPDATE_CHANNEL="$CHANNEL" scripts/validate-linux-distros.sh ubuntu fedora
    REQUIRE_SIGNING=1 scripts/sign-linux-release-artifacts.sh "$ROOT_DIR/dist/linux-$arch"
    core_dirs+=("dist/linux-$arch")

    echo "== clambhook-ui packages: $arch =="
    CLAMBHOOK_LINUX_PLATFORM="$platform" VERSION="$VERSION" \
        "$APPS_DIR/scripts/release-linux-ui.sh" "$arch"
    ui_dirs+=("dist/linux-ui-$arch")
done

echo "== verify packages and generate the repository (container, no key) =="
rm -rf dist/linux-repo
mkdir -p dist/linux-repo
container_inputs=()
for dir in "${core_dirs[@]}"; do container_inputs+=("/core/$dir"); done
for dir in "${ui_dirs[@]}"; do container_inputs+=("/apps/$dir"); done
# shellcheck disable=SC2016 # Expanded by bash inside the container.
"$engine" run --rm \
    --volume "$ROOT_DIR:/core:ro${mount_suffix}" \
    --volume "$APPS_DIR/dist:/apps/dist:ro${mount_suffix}" \
    --volume "$ROOT_DIR/dist/linux-repo:/out${mount_suffix}" \
    --env "VERSION=$VERSION" --env "UPDATE_CHANNEL=$CHANNEL" \
    docker.io/library/ubuntu:24.04 bash -c '
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null
apt-get install -y -qq apt-utils createrepo-c rpm debsig-verify gnupg >/dev/null
CLAMBHOOK_REPO_STAGE=generate REQUIRE_SIGNING=0 \
    /core/scripts/build-linux-repos.sh "$1" /out "${@:2}"
' bash "${container_inputs[@]}"

echo "== sign the repository (host) =="
first_input="${core_dirs[0]}"
CLAMBHOOK_REPO_STAGE=sign REQUIRE_SIGNING=1 VERSION="$VERSION" UPDATE_CHANNEL="$CHANNEL" \
    scripts/build-linux-repos.sh "$first_input" dist/linux-repo

echo "== install from the signed repositories (containers) =="
UPDATE_CHANNEL="$CHANNEL" scripts/test-linux-repo-install.sh dist/linux-repo/clambhook-linux-repo

echo "GNU/Linux release assets are ready in dist/linux-* and $APPS_DIR/dist/linux-ui-*."
echo "Publish with: scripts/publish-release.sh ${VERSION}"
