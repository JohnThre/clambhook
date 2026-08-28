#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

set -euo pipefail

readonly OPENSSL_VERSION="3.5.8"
readonly OPENSSL_SHA256="a8f84a39918ec6415ce765d9b429d313ba97b8143169c172e734b9514464f5b2"
readonly OPENSSL_URL="https://github.com/openssl/openssl/releases/download/openssl-${OPENSSL_VERSION}/openssl-${OPENSSL_VERSION}.tar.gz"

if [[ $# -ne 4 ]]; then
    echo "usage: $0 OUTPUT_ROOT ANDROID_NDK_ROOT API_LEVEL ABI" >&2
    exit 2
fi

output_root="$1"
android_ndk_root="$2"
api_level="${3#android-}"
abi="$4"

if [[ -z "$output_root" || ! -d "$android_ndk_root" ||
      ! "$api_level" =~ ^[0-9]+$ ]]; then
    echo "invalid Android OpenSSL output, NDK, or API argument" >&2
    exit 2
fi

case "$abi" in
    arm64-v8a) openssl_target="android-arm64" ;;
    armeabi-v7a) openssl_target="android-arm" ;;
    x86) openssl_target="android-x86" ;;
    x86_64) openssl_target="android-x86_64" ;;
    *) echo "unsupported Android ABI: $abi" >&2; exit 2 ;;
esac

toolchain_root="$android_ndk_root/toolchains/llvm/prebuilt"
case "$(uname -s)" in
    Darwin) host_tag="darwin-x86_64" ;;
    Linux) host_tag="linux-x86_64" ;;
    *) echo "OpenSSL Android builds require macOS or GNU/Linux" >&2; exit 2 ;;
esac
toolchain_bin="$toolchain_root/$host_tag/bin"
if [[ ! -x "$toolchain_bin/clang" ]]; then
    echo "Android NDK clang not found at $toolchain_bin/clang" >&2
    exit 2
fi

install_root="$output_root/openssl-${OPENSSL_VERSION}-api${api_level}/$abi"
stamp="$install_root/.clambhook-openssl-build"
stamp_value="openssl=${OPENSSL_VERSION} sha256=${OPENSSL_SHA256} api=${api_level} abi=${abi}"
if [[ -f "$stamp" && "$(<"$stamp")" == "$stamp_value" &&
      -f "$install_root/include/openssl/ssl.h" &&
      -f "$install_root/lib/libssl.a" &&
      -f "$install_root/lib/libcrypto.a" ]]; then
    echo "OpenSSL ${OPENSSL_VERSION} for $abi/API $api_level is current"
    exit 0
fi

mkdir -p "$output_root"
lock="$output_root/.openssl-build-lock"
attempt=0
until mkdir "$lock" 2>/dev/null; do
    attempt=$((attempt + 1))
    if (( attempt > 900 )); then
        echo "timed out waiting for Android OpenSSL build lock" >&2
        exit 1
    fi
    sleep 1
done
trap 'rmdir "$lock" 2>/dev/null || true' EXIT

# A parallel ABI build may have completed while this invocation waited.
if [[ -f "$stamp" && "$(<"$stamp")" == "$stamp_value" &&
      -f "$install_root/include/openssl/ssl.h" &&
      -f "$install_root/lib/libssl.a" &&
      -f "$install_root/lib/libcrypto.a" ]]; then
    echo "OpenSSL ${OPENSSL_VERSION} for $abi/API $api_level is current"
    exit 0
fi

downloads="$output_root/downloads"
archive="$downloads/openssl-${OPENSSL_VERSION}.tar.gz"
mkdir -p "$downloads"

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

if [[ ! -f "$archive" || "$(sha256_file "$archive")" != "$OPENSSL_SHA256" ]]; then
    partial="$archive.part.$$"
    trap 'rm -f "$partial"; rmdir "$lock" 2>/dev/null || true' EXIT
    curl --fail --location --silent --show-error --retry 3 \
        --output "$partial" "$OPENSSL_URL"
    actual_sha256="$(sha256_file "$partial")"
    if [[ "$actual_sha256" != "$OPENSSL_SHA256" ]]; then
        echo "OpenSSL archive checksum mismatch: $actual_sha256" >&2
        exit 1
    fi
    mv "$partial" "$archive"
    trap 'rmdir "$lock" 2>/dev/null || true' EXIT
fi

build_root="$output_root/build/openssl-${OPENSSL_VERSION}-api${api_level}/$abi"
install_partial="$install_root.part.$$"
rm -rf "$build_root" "$install_partial"
mkdir -p "$build_root" "$install_partial"
tar -xzf "$archive" --strip-components=1 -C "$build_root"

jobs=4
if command -v nproc >/dev/null 2>&1; then
    jobs="$(nproc)"
elif command -v sysctl >/dev/null 2>&1; then
    jobs="$(sysctl -n hw.logicalcpu 2>/dev/null || echo 4)"
fi

echo "Building OpenSSL ${OPENSSL_VERSION} for $abi/API $api_level"
(
    cd "$build_root"
    export ANDROID_NDK_ROOT="$android_ndk_root"
    export PATH="$toolchain_bin:$PATH"
    ./Configure "$openssl_target" "-D__ANDROID_API__=${api_level}" \
        -Wno-macro-redefined no-shared no-tests no-apps no-docs no-legacy \
        no-weak-ssl-ciphers no-module no-dso no-quic no-autoload-config \
        "--prefix=$install_partial" --libdir=lib >/dev/null
    make -s -j"$jobs" build_sw
    make -s install_sw
)

test -f "$install_partial/include/openssl/ssl.h"
test -f "$install_partial/lib/libssl.a"
test -f "$install_partial/lib/libcrypto.a"
printf '%s\n' "$stamp_value" >"$install_partial/.clambhook-openssl-build"
rm -rf "$install_root"
mv "$install_partial" "$install_root"
rm -rf "$build_root"
echo "Installed OpenSSL ${OPENSSL_VERSION} for $abi/API $api_level"
