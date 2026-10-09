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
apt-get install -y -qq clambhook >/dev/null
dpkg-query -W -f "\${Status}\n" clambhook | grep -q "install ok installed"
test -f /usr/share/keyrings/clambhook-archive-keyring.asc
test -f /etc/apt/sources.list.d/clambhook.sources
apt-get purge -y -qq clambhook >/dev/null
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
cat >/etc/yum.repos.d/clambhook-test.repo <<EOF
[clambhook-test]
name=ClambHook test
baseurl=file:///tmp/repo/rpm/$CHANNEL/\$basearch
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=file:///tmp/repo/clambhook-release-key.asc
EOF
dnf --setopt=tsflags= install -y -q --repo=clambhook-test --repo=fedora --repo=updates clambhook
rpm -q clambhook
test -f /etc/yum.repos.d/clambhook.repo
test -f /etc/pki/rpm-gpg/RPM-GPG-KEY-clambhook
dnf remove -y -q clambhook
# Tampered metadata must be rejected.
arch_dir="/tmp/repo/rpm/$CHANNEL/$(uname -m)"
printf "\n<!-- tampered -->\n" >>"$arch_dir/repodata/repomd.xml"
dnf clean all -q
if dnf makecache -q --repo=clambhook-test 2>/tmp/dnf-tamper.log; then
  echo "dnf accepted tampered repomd.xml" >&2
  exit 1
fi
echo "fedora: signed dnf repository install OK; tampered metadata rejected"'

for target in "${targets[@]}"; do
    case "$target" in
        ubuntu) image="docker.io/library/ubuntu:24.04"; script="$ubuntu_script" ;;
        fedora) image="registry.fedoraproject.org/fedora:44"; script="$fedora_script" ;;
        *) echo "Unknown target: $target (known: ubuntu fedora)" >&2; exit 2 ;;
    esac
    echo "==================== $target repository install ===================="
    "$engine" run --rm \
        --env "CHANNEL=$CHANNEL" \
        --volume "$REPO_DIR:/repo:ro${mount_suffix}" \
        "$image" bash -c "$script"
done
echo "Signed repository install tests passed."
