#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Build and smoke-test ClambHook only on the supported GNU/Linux validation
# matrix: Ubuntu 24.04 LTS and Fedora Linux 44.
#
#   scripts/validate-linux-distros.sh            # both targets
#   scripts/validate-linux-distros.sh ubuntu     # one target
#
# The harness supports Podman or Docker. It validates the sanitizer-backed C17
# runtime, the self-contained JavaFX/Gluon native image, and the authoritative
# distro-family package recipe. No artifact is published.
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
  libasound2-dev libavcodec-dev libavformat-dev libavutil-dev \
  libfreetype6-dev libgl-dev libglib2.0-dev libgtk-3-dev \
  libpango1.0-dev libx11-dev libxtst-dev zlib1g-dev \
  xvfb xauth dbus-x11 gnome-keyring libsecret-tools \
  openjdk-17-jdk-headless maven \
  debhelper dpkg-dev fakeroot rsync iproute2 polkitd systemd \
  git curl wget ca-certificates tar file xz-utils >/dev/null'

rpm_setup='dnf install -y -q --allowerasing \
  gcc gcc-c++ make cmake ninja-build pkgconf-pkg-config \
  libasan libubsan \
  libuv-devel libsodium-devel openssl-devel libcurl-devel \
  alsa-lib-devel "pkgconfig(libavcodec)" "pkgconfig(libavformat)" \
  "pkgconfig(libavutil)" freetype-devel gtk3-devel libX11-devel libXtst-devel \
  mesa-libGL-devel pango-devel zlib-devel \
  xorg-x11-server-Xvfb xorg-x11-xauth dbus-daemon gnome-keyring libsecret \
  maven \
  rpm-build systemd-rpm-macros polkit-devel iproute \
  git curl wget tar gzip file which rsync ca-certificates >/dev/null'

# shellcheck disable=SC2016 # Expanded by bash inside the target container.
smoke='set -euo pipefail
cd /src
GRAALVM_HOME=$(bash scripts/provision-graalvm17.sh /opt/clambhook-graalvm17)
export GRAALVM_HOME JAVA_HOME=$GRAALVM_HOME PATH=$GRAALVM_HOME/bin:$PATH
make test-native
make test-javafx
make build
make build-linux
case "$(uname -m)" in
  x86_64) GLUON_TARGET=x86_64-linux ;;
  aarch64|arm64) GLUON_TARGET=aarch64-linux ;;
  *) echo "unsupported JavaFX image architecture"; exit 2 ;;
esac
GLUON_UI="ui/javafx/target/gluonfx/$GLUON_TARGET/clambhook-ui"
test -x "$GLUON_UI"
CLAMBHOOK_UI_CONFIG=$(mktemp -d)
set +e
timeout 4s xvfb-run -a env \
  XDG_CONFIG_HOME="$CLAMBHOOK_UI_CONFIG" \
  CLAMBHOOK_API_URL=http://127.0.0.1:1 \
  "$GLUON_UI" >/tmp/clambhook-javafx-smoke.log 2>&1
CLAMBHOOK_UI_EXIT=$?
set -e
test "$CLAMBHOOK_UI_EXIT" -eq 124
! ldd "$GLUON_UI" | grep -Eq "libjvm|/jre/|/jdk/"
SNAP=$(echo "{\"command\":\"ensure-trial\",\"snapshot\":\"\"}" | ./build-native/clambhook-license)
echo "license: $SNAP"
echo "$SNAP" | grep -q "\"ok\":true"
./build-native/clambhook -version
./build-native/clambhook-tui -version
! readelf -S ./build-native/clambhook | grep -q "\.go\.buildinfo"
echo "ClambHook C17 and JavaFX/Gluon smoke: OK"'

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
    "$image" bash -lc "$setup; $command; $recipe"
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
