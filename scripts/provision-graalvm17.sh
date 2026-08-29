#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

set -euo pipefail

VERSION=17.0.9
BASE_URL="https://github.com/graalvm/graalvm-ce-builds/releases/download/jdk-${VERSION}"
INSTALL_DIR="${1:-}"

if [[ -z "$INSTALL_DIR" || "$INSTALL_DIR" != /* || "$INSTALL_DIR" == "/" ]]; then
    echo "usage: scripts/provision-graalvm17.sh /absolute/empty/directory" >&2
    exit 2
fi
if [[ -d "$INSTALL_DIR" && -n "$(find "$INSTALL_DIR" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
    echo "GraalVM destination is not empty: $INSTALL_DIR" >&2
    exit 2
fi

case "$(uname -s)-$(uname -m)" in
    Linux-x86_64)
        platform=linux-x64
        sha256=e47ba7229cef02393e19d5b8f46f7f1cab4829dd17bfe84d5431fc8ff0e22a96
        home_suffix=
        ;;
    Linux-aarch64|Linux-arm64)
        platform=linux-aarch64
        sha256=c3281b21f5220c2f76cf6fa0d646bc42e2d729af2c022bb06e557a613ba16102
        home_suffix=
        ;;
    Darwin-arm64|Darwin-aarch64)
        platform=macos-aarch64
        sha256=3eccc4ffda01818172b7fc7cdf4379bc62ed7129ee30ca854c04da67057249c9
        home_suffix=/Contents/Home
        ;;
    *)
        echo "unsupported GraalVM 17 provisioning host: $(uname -s) $(uname -m)" >&2
        exit 2
        ;;
esac

if [[ ! "$sha256" =~ ^[0-9a-f]{64}$ ]]; then
    echo "invalid pinned GraalVM SHA-256 for $platform" >&2
    exit 2
fi

archive_name="graalvm-community-jdk-${VERSION}_${platform}_bin.tar.gz"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-graalvm17.XXXXXX")"
trap 'rm -rf "$work_dir"' EXIT
archive="$work_dir/$archive_name"

curl --fail --location --silent --show-error --retry 5 \
    --output "$archive" "$BASE_URL/$archive_name"
if command -v sha256sum >/dev/null 2>&1; then
    printf '%s  %s\n' "$sha256" "$archive" | sha256sum --check -
else
    printf '%s  %s\n' "$sha256" "$archive" | shasum -a 256 --check -
fi
mkdir -p "$INSTALL_DIR"
tar -xzf "$archive" -C "$INSTALL_DIR" --strip-components=1

graalvm_home="$INSTALL_DIR$home_suffix"
"$graalvm_home/bin/java" -version >&2
"$graalvm_home/bin/native-image" --version >&2
printf '%s\n' "$graalvm_home"
