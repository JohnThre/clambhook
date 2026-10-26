#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Prove that apt (Ubuntu 24.04) and dnf (Fedora 44) install ClambHook from the
# signed repositories with signature checking enabled, and that both refuse
# repository metadata that no longer matches its developer@jpfchang.org
# signature. Runs only inside throwaway Podman/Docker containers.
#
#   UPDATE_CHANNEL=stable scripts/test-linux-repo-install.sh <repo-dir> [ubuntu] [fedora]
#
# <repo-dir> is the clambhook-linux-repo directory from build-linux-repos.sh.
# When the repository carries clambhook-ui, installing it must pull in the
# matching clambhook core package. With CLAMBHOOK_UPGRADE_FROM set to a
# directory holding a combined 1.0.x .deb/.rpm, the test first installs that
# package and proves that upgrading clambhook keeps the desktop controller.
set -euo pipefail

REPO_DIR="$(cd "${1:?repository directory is required}" && pwd)"
shift
CHANNEL="${UPDATE_CHANNEL:-stable}"
targets=("$@")
(( ${#targets[@]} > 0 )) || targets=(ubuntu fedora)

engine=""
mount_suffix=""
if command -v podman >/dev/null 2>&1; then
    engine="podman"
    mount_suffix=":Z"
elif command -v docker >/dev/null 2>&1; then
    engine="docker"
else
    echo "Need podman or docker for repository install tests." >&2
    exit 2
fi

# shellcheck disable=SC2016 # Expanded by bash inside the target container.
ubuntu_script='set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null
cp -a /repo /tmp/repo
# apt verifies as the unprivileged _apt user; host modes may not survive the mount.
chmod -R a+rX /tmp/repo
cat >/etc/apt/sources.list.d/clambhook-test.sources <<EOF
Types: deb
URIs: file:/tmp/repo/apt
Suites: $CHANNEL
Components: main
Signed-By: /tmp/repo/clambhook-release-key.asc
EOF
apt-get update -o Dir::Etc::sourcelist=/dev/null \
    -o Dir::Etc::sourceparts=/etc/apt/sources.list.d \
    -o APT::Update::Error-Mode=any -qq
old_deb=$(find /upgrade-from -maxdepth 1 -name "*.deb" -print -quit 2>/dev/null || true)
if [[ -n "$old_deb" ]]; then
  apt-get install -y -qq "$old_deb" >/dev/null
  apt-get --only-upgrade install -y -qq clambhook >/dev/null
  dpkg-query -W -f "\${Status}\n" clambhook-ui | grep -q "install ok installed"
  test -x /usr/bin/clambhook-ui
  apt-get purge -y -qq clambhook-ui clambhook >/dev/null
fi
if apt-cache show clambhook-ui >/dev/null 2>&1; then
  apt-get install -y -qq clambhook-ui >/dev/null
  dpkg-query -W -f "\${Status}\n" clambhook-ui | grep -q "install ok installed"
else
  apt-get install -y -qq clambhook >/dev/null
fi
dpkg-query -W -f "\${Status}\n" clambhook | grep -q "install ok installed"
test -f /usr/share/keyrings/clambhook-archive-keyring.asc
test -f /etc/apt/sources.list.d/clambhook.sources
apt-get purge -y -qq clambhook >/dev/null
if dpkg-query -W -f "\${Status}\n" clambhook-ui 2>/dev/null | grep -q "install ok installed"; then
  echo "removing clambhook left clambhook-ui installed" >&2
  exit 1
fi
# Tampered metadata must be rejected.
rm -rf /var/lib/apt/lists/*_tmp_repo_*
sed -i "s/^Version: .*/Version: 0.0.0-tampered/" "/tmp/repo/apt/dists/$CHANNEL/InRelease"
if apt-get update -o Dir::Etc::sourcelist=/dev/null \
    -o Dir::Etc::sourceparts=/etc/apt/sources.list.d \
    -o APT::Update::Error-Mode=any -qq 2>/tmp/apt-tamper.log; then
  echo "apt accepted tampered InRelease" >&2
  exit 1
fi
grep -Eqi "(BADSIG|not valid|NO_PUBKEY|is not signed)" /tmp/apt-tamper.log
echo "ubuntu: signed apt repository install OK; tampered metadata rejected"'

# shellcheck disable=SC2016 # Expanded by bash inside the target container.
fedora_script='set -euo pipefail
cp -a /repo /tmp/repo
chmod -R a+rX /tmp/repo
cat >/etc/yum.repos.d/clambhook-test.repo <<EOF
[clambhook-test]
name=ClambHook test
baseurl=file:///tmp/repo/rpm/$CHANNEL/\$basearch
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=file:///tmp/repo/clambhook-release-key.asc
EOF
old_rpm=$(find /upgrade-from -maxdepth 1 -name "*.rpm" -print -quit 2>/dev/null || true)
if [[ -n "$old_rpm" ]]; then
  dnf --setopt=tsflags= install -y -q --nogpgcheck "$old_rpm"
  dnf --setopt=tsflags= upgrade -y -q --repo=clambhook-test --repo=fedora --repo=updates clambhook
  rpm -q clambhook-ui
  test -x /usr/bin/clambhook-ui
  dnf remove -y -q clambhook-ui clambhook
fi
if ls /tmp/repo/rpm/$CHANNEL/$(uname -m)/clambhook-ui-*.rpm >/dev/null 2>&1; then
  dnf --setopt=tsflags= install -y -q --repo=clambhook-test --repo=fedora --repo=updates clambhook-ui
  rpm -q clambhook-ui
else
  dnf --setopt=tsflags= install -y -q --repo=clambhook-test --repo=fedora --repo=updates clambhook
fi
rpm -q clambhook
test -f /etc/yum.repos.d/clambhook.repo
test -f /etc/pki/rpm-gpg/RPM-GPG-KEY-clambhook
dnf remove -y -q clambhook
if rpm -q clambhook-ui >/dev/null 2>&1; then
  echo "removing clambhook left clambhook-ui installed" >&2
  exit 1
fi
# dnf5 (Fedora 44) does not enforce repo_gpgcheck: it accepts a tampered,
# garbage-signed or unsigned repomd.xml. Packages stay protected by their
# embedded signatures (pkg_gpgcheck); check the metadata signature with gpg.
arch_dir="/tmp/repo/rpm/$CHANNEL/$(uname -m)"
export GNUPGHOME="$(mktemp -d)"
gpg --batch -q --import /tmp/repo/clambhook-release-key.asc
gpg --batch -q --verify "$arch_dir/repodata/repomd.xml.asc" "$arch_dir/repodata/repomd.xml"
printf "\n<!-- tampered -->\n" >>"$arch_dir/repodata/repomd.xml"
if gpg --batch -q --verify "$arch_dir/repodata/repomd.xml.asc" "$arch_dir/repodata/repomd.xml" 2>/dev/null; then
  echo "tampered repomd.xml still verifies" >&2
  exit 1
fi
echo "fedora: signed dnf repository install OK; repomd.xml signature verified, tampering detected"'

for target in "${targets[@]}"; do
    case "$target" in
        ubuntu) image="docker.io/library/ubuntu:24.04"; script="$ubuntu_script" ;;
        fedora) image="registry.fedoraproject.org/fedora:44"; script="$fedora_script" ;;
        *) echo "Unknown target: $target (known: ubuntu fedora)" >&2; exit 2 ;;
    esac
    echo "==================== $target repository install ===================="
    upgrade_mount=()
    if [[ -n "${CLAMBHOOK_UPGRADE_FROM:-}" ]]; then
        upgrade_mount=(--volume "$(cd "$CLAMBHOOK_UPGRADE_FROM" && pwd):/upgrade-from:ro${mount_suffix/:/,}")
    fi
    "$engine" run --rm \
        --env "CHANNEL=$CHANNEL" \
        --volume "$REPO_DIR:/repo:ro${mount_suffix/:/,}" \
        ${upgrade_mount[@]+"${upgrade_mount[@]}"} \
        "$image" bash -c "$script"
done
echo "Signed repository install tests passed."
