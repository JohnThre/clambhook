#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

set -euo pipefail

INSTALL_DIR="${1:-}"
DISTRIBUTION="${2:-community}"

if [[ -z "$INSTALL_DIR" || "$INSTALL_DIR" != /* || "$INSTALL_DIR" == "/" ]]; then
    echo "usage: scripts/provision-graalvm17.sh /absolute/empty/directory [community|gluon]" >&2
    exit 2
fi
if [[ -d "$INSTALL_DIR" && -n "$(find "$INSTALL_DIR" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
    echo "GraalVM destination is not empty: $INSTALL_DIR" >&2
    exit 2
fi

case "$DISTRIBUTION" in
    community)
        version=17.0.9
        base_url="https://github.com/graalvm/graalvm-ce-builds/releases/download/jdk-${version}"
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
                echo "unsupported GraalVM Community 17 provisioning host: $(uname -s) $(uname -m)" >&2
                exit 2
                ;;
        esac
        archive_name="graalvm-community-jdk-${version}_${platform}_bin.tar.gz"
        ;;
    gluon)
        version=22.1.0.1-Final
        base_url="https://github.com/gluonhq/graal/releases/download/gluon-${version}"
        case "$(uname -s)-$(uname -m)" in
            Linux-x86_64)
                archive_name="graalvm-svm-java17-linux-gluon-${version}.tar.gz"
                sha256=70df79831e4e55289414b4e9c4ab78b74e31d7b7db7ba70cfff86ab8f9f8d4ef
                home_suffix=
                ;;
            *)
                echo "the pinned Gluon GraalVM 17 Android toolchain requires a Linux x86_64 host" >&2
                exit 2
                ;;
        esac
        ;;
    *)
        echo "unknown GraalVM distribution: $DISTRIBUTION (expected community or gluon)" >&2
        exit 2
        ;;
esac

if [[ ! "$sha256" =~ ^[0-9a-f]{64}$ ]]; then
    echo "invalid pinned GraalVM SHA-256 for $platform" >&2
    exit 2
fi

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-graalvm17.XXXXXX")"
trap 'rm -rf "$work_dir"' EXIT
archive="$work_dir/$archive_name"

curl --fail --location --silent --show-error --retry 5 \
    --output "$archive" "$base_url/$archive_name"
if command -v sha256sum >/dev/null 2>&1; then
    printf '%s  %s\n' "$sha256" "$archive" | sha256sum --check - >&2
else
    printf '%s  %s\n' "$sha256" "$archive" | shasum -a 256 --check - >&2
fi
mkdir -p "$INSTALL_DIR"
tar -xzf "$archive" -C "$INSTALL_DIR" --strip-components=1

graalvm_home="$INSTALL_DIR$home_suffix"
"$graalvm_home/bin/java" -version >&2
"$graalvm_home/bin/native-image" --version >&2
printf '%s\n' "$graalvm_home"
