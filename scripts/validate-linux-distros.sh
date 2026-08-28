#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Build and smoke-test ClambHook only on the supported GNU/Linux validation
# matrix: Trisquel 12, Rocky Linux 9, and AlmaLinux 9.
#
# Trisquel does not publish an OCI image. On arm64 the harness uses Trisquel's
# official checksum-pinned base root filesystem. On x86_64 it uses debootstrap
# with Trisquel's official archive key and repositories.
# Rocky Linux and AlmaLinux use their official version-9 container images.
#
#   scripts/validate-linux-distros.sh            # all three targets
#   scripts/validate-linux-distros.sh trisquel   # one target
#
# The harness supports Podman or Docker. It validates the sanitizer-backed C runtime, C/GTK client, the
# legacy rollback runtime while migration is active, desktop controller tests,
# and the distro-family package recipe. No artifact is published.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

engine=""
mount_suffix=""
if command -v podman >/dev/null 2>&1; then
  engine="podman"
  mount_suffix=":Z"
elif command -v docker >/dev/null 2>&1; then
  engine="docker"
else
  echo "Need podman or docker to validate GNU/Linux locally." >&2
  exit 2
fi

declare -A IMAGE=(
  [trisquel]="clambhook-ci/trisquel:12.0"
  [rocky]="docker.io/rockylinux/rockylinux:9"
  [alma]="docker.io/library/almalinux:9"
)

trisquel_image_ready=0
prepare_trisquel_image() {
  if [[ "$trisquel_image_ready" == "1" ]]; then
    return
  fi

  command -v curl >/dev/null 2>&1 || {
    echo "curl is required to provision the official Trisquel rootfs." >&2
    return 2
  }

  local workdir rootfs checksum image keyring rootdir fingerprint
  workdir="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-trisquel-ci.XXXXXX")"
  rootfs="$workdir/trisquel-rootfs.tar.bz2"
  image="${IMAGE[trisquel]}"

  case "$(uname -m)" in
    arm64|aarch64)
      checksum="a241899069ca300eb54fadb06a1107f9d218add00d5f76387a8121ba1f23bf46"
      curl --fail --location --silent --show-error --retry 3 \
        --output "$rootfs" \
        "https://cdbuilds.trisquel.org/ecne/trisquel-base_12.0_arm64.tar.bz2"
      printf '%s  %s\n' "$checksum" "$rootfs" | sha256sum --check -
      ;;
    x86_64|amd64)
      command -v debootstrap >/dev/null 2>&1 || {
        echo "debootstrap is required to construct the official Trisquel x86_64 image." >&2
        return 2
      }
      command -v sudo >/dev/null 2>&1 || {
        echo "sudo is required to construct the official Trisquel x86_64 image." >&2
        return 2
      }
      command -v gpg >/dev/null 2>&1 || {
        echo "gpg is required to authenticate the official Trisquel archive key." >&2
        return 2
      }
      keyring="$workdir/trisquel-archive-signkey.gpg"
      rootdir="$workdir/rootfs"
      fingerprint="60364C9869F92450421F0C22B138CA450C05112F"
      curl --fail --location --silent --show-error --retry 3 \
        --output "$keyring" \
        "https://archive.trisquel.info/trisquel/trisquel-archive-signkey.gpg"
      gpg --batch --no-default-keyring --keyring "$keyring" \
        --with-colons --fingerprint "$fingerprint" | \
        grep -q "^fpr:::::::::${fingerprint}:$"
      sudo debootstrap \
        --arch=amd64 \
        --variant=minbase \
        --keyring="$keyring" \
        ecne "$rootdir" \
        https://archive.trisquel.info/trisquel \
        /usr/share/debootstrap/scripts/noble
      sudo tar --numeric-owner -C "$rootdir" -cjf "$rootfs" .
      sudo rm -rf "$rootdir"
      ;;
    *)
      echo "unsupported Trisquel container architecture: $(uname -m)" >&2
      return 2
      ;;
  esac

  cp "$repo_root/packaging/ci/trisquel.Containerfile" "$workdir/Containerfile"
  "$engine" build --tag "$image" --file "$workdir/Containerfile" "$workdir"
  rm -rf "$workdir"
  trisquel_image_ready=1
}

apt_setup='export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq \
  build-essential cmake ninja-build pkg-config \
  libuv1-dev libsodium-dev libllhttp-dev libssl-dev libcurl4-openssl-dev \
  libgtk-4-dev libsoup-3.0-dev libjson-glib-dev \
  xvfb xauth libsecret-tools \
  openjdk-17-jdk-headless \
  debhelper dh-golang dpkg-dev fakeroot rsync \
  git curl wget ca-certificates tar file xz-utils >/dev/null'

rpm_setup='dnf install -y -q dnf-plugins-core epel-release >/dev/null
dnf config-manager --set-enabled crb >/dev/null
dnf install -y -q --allowerasing \
  gcc gcc-c++ make cmake ninja-build pkgconf-pkg-config \
  libuv-devel libsodium-devel llhttp-devel openssl-devel libcurl-devel \
  gtk4-devel libsoup3-devel json-glib-devel \
  xorg-x11-server-Xvfb xorg-x11-xauth libsecret \
  java-17-openjdk-devel \
  rpm-build systemd-rpm-macros polkit-devel \
  git curl wget tar gzip file which rsync ca-certificates >/dev/null'

# Stock distro Go packages are older than go.mod, so install the exact official
# toolchain requested by the repository.
# shellcheck disable=SC2016 # Expanded by bash inside the target container.
go_setup='set -e
GO_VER=$(sed -n "s/^go \([0-9.][0-9.]*\)$/\1/p" go.mod | head -1)
case "$(uname -m)" in
  x86_64) GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  *) echo "unsupported architecture $(uname -m)"; exit 2 ;;
esac
curl -fsSL "https://go.dev/dl/go${GO_VER}.linux-${GOARCH}.tar.gz" | tar -C /usr/local -xz
export PATH=/usr/local/go/bin:$PATH
go version'

# shellcheck disable=SC2016 # Expanded by bash inside the target container.
smoke='set -e
cd /src
export PATH=/usr/local/go/bin:$PATH
make test-native
make build-linux-gtk
test -x build-native/clambhook-linux-c
./build-native/clambhook-linux-c --version | grep -q "^clambhook-linux "
CLAMBHOOK_GTK_LICENSE_CONFIG=$(mktemp -d)
set +e
timeout 4s xvfb-run -a env \
  XDG_CONFIG_HOME="$CLAMBHOOK_GTK_LICENSE_CONFIG" \
  CLAMBHOOK_API_URL=http://127.0.0.1:1 \
  ./build-native/clambhook-linux-c >/tmp/clambhook-gtk-smoke.log 2>&1
CLAMBHOOK_GTK_EXIT=$?
set -e
test "$CLAMBHOOK_GTK_EXIT" -eq 124
test "$(stat -c %a "$CLAMBHOOK_GTK_LICENSE_CONFIG/clambhook/linux-license.json")" = 600
test "$(stat -c %a "$CLAMBHOOK_GTK_LICENSE_CONFIG/clambhook/license-snapshot.json")" = 600
grep -q "trialStartDate" "$CLAMBHOOK_GTK_LICENSE_CONFIG/clambhook/license-snapshot.json"
make test
make build
make test-linux
SNAP=$(echo "{\"command\":\"ensure-trial\",\"snapshot\":\"\"}" | ./bin/clambhook-license)
echo "license: $SNAP"
echo "$SNAP" | grep -q "\"ok\":true"
./bin/clambhook -version
./bin/clambhook-tui -version
echo "ClambHook C, GTK, rollback, desktop, and license smoke: OK"'

run_one() {
  local distro="$1" image="${IMAGE[$1]:-}" setup recipe command
  local -a container_env=()
  if [[ -z "$image" ]]; then
    echo "Unknown distro: $distro (known: trisquel rocky alma)" >&2
    return 2
  fi

  case "$distro" in
    trisquel)
      prepare_trisquel_image
      setup="$apt_setup"
      recipe='./scripts/ci-linux-package-recipes.sh debian'
      ;;
    rocky|alma)
      setup="$rpm_setup"
      recipe='./scripts/ci-linux-package-recipes.sh rpm'
      ;;
  esac

  if [[ "${CLAMBHOOK_LINUX_RELEASE_BUILD:-0}" == "1" ]]; then
    [[ -n "${VERSION:-}" ]] || {
      echo "VERSION is required for containerized release builds." >&2
      return 2
    }
    container_env+=(--env "VERSION=$VERSION")
    container_env+=(--env "UPDATE_CHANNEL=${UPDATE_CHANNEL:-stable}")
    case "$distro" in
      trisquel)
        command='make clean; REQUIRE_SIGNING=0 CLAMBHOOK_RELEASE_APPEND=0 scripts/release-linux.sh deb'
        ;;
      rocky)
        command='make clean; REQUIRE_SIGNING=0 CLAMBHOOK_RELEASE_APPEND=1 scripts/release-linux.sh rpm'
        ;;
      alma)
        echo "AlmaLinux is a validation lane; release RPMs are built on Rocky Linux." >&2
        return 2
        ;;
    esac
    recipe=':'
  else
    command="$smoke"
  fi

  echo "==================== $distro ($image) ===================="
  "$engine" run --rm \
    "${container_env[@]}" \
    --volume "$repo_root:/src${mount_suffix}" \
    --workdir /src \
    "$image" bash -lc "$setup; $go_setup; $command; $recipe"
  echo "==================== $distro: PASS ===================="
}

targets=("$@")
if [[ ${#targets[@]} -eq 0 ]]; then
  targets=(trisquel rocky alma)
fi

failed=()
for distro in "${targets[@]}"; do
  if ! run_one "$distro"; then
    failed+=("$distro")
  fi
done

if [[ ${#failed[@]} -gt 0 ]]; then
  echo "FAILED: ${failed[*]}" >&2
  exit 1
fi
echo "All requested GNU/Linux targets validated."
