#!/usr/bin/env bash
# Headless build + smoke validation of the ClambHook GNU/Linux app across the
# six supported distributions, using throwaway Linux containers. On macOS, this
# prefers Apple's `container` tool (https://github.com/apple/container), which
# runs OCI Linux containers inside lightweight VMs on Apple silicon. On Linux,
# it falls back to podman or docker.
#
#   scripts/validate-linux-distros.sh            # all distros
#   scripts/validate-linux-distros.sh fedora     # one distro
#
# For each distro the harness installs the build toolchain, builds the daemon +
# terminal UI + license helper + Kotlin/Compose Multiplatform desktop
# controller, then smoke-tests headlessly:
#   1. clambhook-license seeds a trial and evaluates it (JSON ok, reason trial)
#   2. clambhook -version runs
#   3. clambhook-tui -version runs
# GUI rendering is out of scope for headless containers; it is covered by the
# Gradle test suite and manual QA on a desktop.
#
# PureOS is Debian-based and is validated through the Debian package path.
# Bazzite is Fedora/atomic and is validated through the Fedora build plus the
# Flatpak manifest (packaging/flatpak), which is the supported Bazzite channel.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

engine=""
mount_suffix=""
if command -v container >/dev/null 2>&1; then
  engine="container"
elif command -v podman >/dev/null 2>&1; then
  engine="podman"
  mount_suffix=":Z"
elif command -v docker >/dev/null 2>&1; then
  engine="docker"
else
  echo "Need Apple container, podman, or docker to validate distro containers." >&2
  echo "On macOS: install https://github.com/apple/container and run: container system start" >&2
  exit 2
fi

# Apple's container tool needs its background service running before use.
if [[ "$engine" == "container" ]]; then
  if ! container ls >/dev/null 2>&1; then
    echo "Apple container service is not running. Start it with: container system start" >&2
    exit 2
  fi
fi

# distro -> image
declare -A IMAGE=(
  [ubuntu]="docker.io/library/ubuntu:24.04"
  [debian]="docker.io/library/debian:12"
  [pureos]="docker.io/library/debian:12"   # PureOS is Debian-based
  [fedora]="docker.io/library/fedora:41"
  [rocky]="docker.io/library/rockylinux:9"
  [bazzite]="docker.io/library/fedora:41"  # Bazzite is Fedora-based; Flatpak channel
)

apt_setup='export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get install -y -qq \
  gcc make pkg-config libsodium-dev \
  default-jdk-headless \
  debhelper dh-golang dpkg-dev fakeroot rsync git curl wget ca-certificates tar file >/dev/null'

dnf_setup='dnf install -y -q gcc make rpm-build pkgconf-pkg-config \
  java-17-openjdk-devel libsodium-devel systemd-rpm-macros polkit-devel \
  git curl tar gzip file which >/dev/null'

# Stock distro Go packages are older than the go.mod requirement, so install the
# pinned official Go toolchain (the exact version from go.mod) into /usr/local.
go_setup='set -e
GO_VER=$(sed -n "s/^go \([0-9.][0-9.]*\)$/\1/p" go.mod | head -1)
case "$(uname -m)" in
  x86_64) GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  *) echo "unsupported arch $(uname -m)"; exit 2 ;;
esac
curl -fsSL "https://go.dev/dl/go${GO_VER}.linux-${GOARCH}.tar.gz" | tar -C /usr/local -xz
export PATH=/usr/local/go/bin:$PATH
go version'

smoke='set -e; cd /src
export PATH=/usr/local/go/bin:$PATH
make build >/build.log 2>&1 || { tail -40 /build.log; exit 1; }
make build-linux >/dev/null 2>&1 || { echo "Compose controller build failed"; exit 1; }
SNAP=$(echo "{\"command\":\"ensure-trial\",\"snapshot\":\"\"}" | ./bin/clambhook-license)
echo "license: $SNAP"
echo "$SNAP" | grep -q "\"ok\":true" || { echo "license helper failed"; exit 1; }
./bin/clambhook -version || true
./bin/clambhook-tui -version || true
echo "OK"'

run_one() {
  local distro="$1" image="${IMAGE[$1]:-}"
  if [[ -z "$image" ]]; then
    echo "Unknown distro: $distro (known: ${!IMAGE[*]})" >&2
    return 2
  fi
  local setup="$apt_setup" recipe=""
  case "$distro" in
    ubuntu)
      setup="$apt_setup; apt-get install -y -qq flatpak flatpak-builder >/dev/null"
      recipe='./scripts/ci-linux-package-recipes.sh debian; ./scripts/ci-linux-package-recipes.sh portable'
      ;;
    debian|pureos)
      recipe='./scripts/ci-linux-package-recipes.sh debian'
      ;;
    fedora|bazzite)
      setup="$dnf_setup"
      recipe='./scripts/ci-linux-package-recipes.sh rpm'
      ;;
    rocky)
      setup='dnf install -y -q epel-release >/dev/null; '
      setup+="$dnf_setup"
      recipe='./scripts/ci-linux-package-recipes.sh rpm'
      ;;
  esac
  echo "==================== $distro ($image) ===================="
  "$engine" run --rm -v "$repo_root":/src${mount_suffix} -w /src "$image" \
    bash -lc "$setup; $go_setup; $smoke; $recipe"
  echo "==================== $distro: PASS ===================="
}

targets=("$@")
if [[ ${#targets[@]} -eq 0 ]]; then
  targets=(ubuntu debian pureos fedora rocky bazzite)
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
echo "All requested distros validated."
