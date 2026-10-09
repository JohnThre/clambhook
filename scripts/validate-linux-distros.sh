#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Build and smoke-test ClambHook only on the supported GNU/Linux validation
# matrix: Ubuntu 24.04 LTS and Fedora Linux 44.
#
#   scripts/validate-linux-distros.sh            # both targets
#   scripts/validate-linux-distros.sh ubuntu     # one target
#
# Ubuntu and Fedora are the only supported GNU/Linux distributions. The harness
# supports Podman or Docker. It validates the sanitizer-backed C17 runtime, the
# self-contained Kotlin/Compose desktop distributable (private jlink runtime),
# and the authoritative distro-family package recipe. No artifact is published.
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
  [ubuntu]="docker.io/library/ubuntu:24.04"
  [fedora]="registry.fedoraproject.org/fedora:44"
)

apt_setup='export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq \
  build-essential cmake ninja-build pkg-config \
  libuv1-dev libsodium-dev libssl-dev libcurl4-openssl-dev \
  openjdk-21-jdk \
  libx11-6 libxext6 libxi6 libxrender1 libxtst6 libfontconfig1 libfreetype6 \
  xvfb xauth dbus-x11 gnome-keyring libsecret-tools \
  debhelper dpkg-dev fakeroot rsync iproute2 polkitd systemd \
  git curl wget ca-certificates gnupg tar file xz-utils python3 unzip >/dev/null'

rpm_setup='dnf install -y -q --allowerasing \
  gcc gcc-c++ make cmake ninja-build pkgconf-pkg-config \
  libasan libubsan \
  libuv-devel libsodium-devel openssl-devel libcurl-devel \
  java-25-openjdk-devel java-25-openjdk-jmods \
  libX11 libXext libXi libXrender libXtst fontconfig freetype \
  xorg-x11-server-Xvfb xorg-x11-xauth dbus-daemon gnome-keyring libsecret \
  rpm-build systemd-rpm-macros polkit-devel iproute \
  git curl wget tar gzip file which rsync ca-certificates gnupg2 python3 unzip >/dev/null'

# shellcheck disable=SC2016 # Expanded by bash inside the target container.
toolchain='JAVA_HOME=$(dirname "$(dirname "$(readlink -f "$(command -v javac)")")")
export JAVA_HOME PATH=$JAVA_HOME/bin:$PATH'

# shellcheck disable=SC2016 # Expanded by bash inside the target container.
smoke='set -euo pipefail
cd /src
make test-native
make test-linux
make build
make build-linux
CLAMBHOOK_UI="ui/kotlin/desktop/build/linux-dist/clambhook-ui/bin/clambhook-ui"
if [[ ! -x "$CLAMBHOOK_UI" ]]; then
  echo "desktop distributable not found: $CLAMBHOOK_UI" >&2
  exit 2
fi
CLAMBHOOK_UI_CONFIG=$(mktemp -d)
set +e
timeout 8s xvfb-run -a env \
  XDG_CONFIG_HOME="$CLAMBHOOK_UI_CONFIG" \
  CLAMBHOOK_API_URL=http://127.0.0.1:1 \
  "$CLAMBHOOK_UI" >/tmp/clambhook-ui-smoke.log 2>&1
CLAMBHOOK_UI_EXIT=$?
set -e
if [[ "$CLAMBHOOK_UI_EXIT" -ne 124 ]]; then
  echo "desktop controller exited unexpectedly: $CLAMBHOOK_UI_EXIT" >&2
  cat /tmp/clambhook-ui-smoke.log >&2
  exit 1
fi
SNAP=$(echo "{\"command\":\"ensure-trial\",\"snapshot\":\"\"}" | ./build-native/clambhook-license)
echo "license: $SNAP"
echo "$SNAP" | grep -q "\"ok\":true"
./build-native/clambhook -version
./build-native/clambhook-tui -version
! readelf -S ./build-native/clambhook | grep -q "\.go\.buildinfo"
echo "ClambHook C17 and Kotlin/Compose desktop smoke: OK"'

run_one() {
  local distro="$1" image="${IMAGE[$1]:-}" setup recipe command
  local -a container_env=()
  if [[ -z "$image" ]]; then
    echo "Unknown distro: $distro (known: ubuntu fedora)" >&2
    return 2
  fi

  case "$distro" in
    ubuntu)
      setup="$apt_setup"
      recipe='./scripts/ci-linux-package-recipes.sh debian'
      ;;
    fedora)
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
      ubuntu)
        # shellcheck disable=SC2016 # Expanded by bash inside the target container.
        command='make clean
REQUIRE_SIGNING=0 CLAMBHOOK_RELEASE_APPEND=0 scripts/release-linux.sh deb
release_package=$(find dist/linux -maxdepth 1 -type f -name "*.deb" -print -quit)
test -n "$release_package"
CLAMBHOOK_CONTAINER_PACKAGE_SMOKE=1 scripts/smoke-installed-linux-package.sh "$release_package"'
        ;;
      fedora)
        # shellcheck disable=SC2016 # Expanded by bash inside the target container.
        command='make clean
REQUIRE_SIGNING=0 CLAMBHOOK_RELEASE_APPEND=1 scripts/release-linux.sh rpm
release_package=$(find dist/linux -maxdepth 1 -type f -name "*.rpm" -print -quit)
test -n "$release_package"
CLAMBHOOK_CONTAINER_PACKAGE_SMOKE=1 scripts/smoke-installed-linux-package.sh "$release_package"'
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
    "$image" bash -lc "$setup; $toolchain; $command; $recipe"
  echo "==================== $distro: PASS ===================="
}

targets=("$@")
if [[ ${#targets[@]} -eq 0 ]]; then
  targets=(ubuntu fedora)
fi

for distro in "${targets[@]}"; do
  run_one "$distro"
done
echo "All requested GNU/Linux targets validated."
