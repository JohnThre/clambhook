#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

set -euo pipefail

readonly CURL_VERSION="8.18.0"
readonly CURL_SHA256="e9274a5f8ab5271c0e0e6762d2fce194d5f98acc568e4ce816845b2dcc0cf88f"
readonly CURL_URL="https://github.com/curl/curl/releases/download/curl-8_18_0/curl-${CURL_VERSION}.tar.gz"

if [[ $# -ne 5 ]]; then
    echo "usage: $0 OUTPUT_ROOT ANDROID_NDK_ROOT API_LEVEL ABI OPENSSL_ROOT" >&2
    exit 2
fi

output_root="$1"
android_ndk_root="$2"
api_level="${3#android-}"
abi="$4"
openssl_root="$5"

if [[ -z "$output_root" || ! -d "$android_ndk_root" ||
      ! "$api_level" =~ ^[0-9]+$ ||
      ! -f "$openssl_root/lib/libssl.a" ||
      ! -f "$openssl_root/lib/libcrypto.a" ]]; then
    echo "invalid Android curl output, NDK, API, or OpenSSL argument" >&2
    exit 2
fi

case "$abi" in
    arm64-v8a|armeabi-v7a|x86|x86_64) ;;
    *) echo "unsupported Android ABI: $abi" >&2; exit 2 ;;
esac

install_root="$output_root/curl-${CURL_VERSION}-api${api_level}/$abi"
stamp="$install_root/.clambhook-curl-build"
stamp_value="curl=${CURL_VERSION} sha256=${CURL_SHA256} recipe=http-only-v2 openssl=3.5.8-quic api=${api_level} abi=${abi}"
if [[ -f "$stamp" && "$(<"$stamp")" == "$stamp_value" &&
      -f "$install_root/include/curl/curl.h" &&
      -f "$install_root/lib/libcurl.a" ]]; then
    echo "curl ${CURL_VERSION} for $abi/API $api_level is current"
    exit 0
fi

mkdir -p "$output_root"
lock="$output_root/.curl-build-api${api_level}-${abi}.lock"
attempt=0
until mkdir "$lock" 2>/dev/null; do
    attempt=$((attempt + 1))
    if (( attempt > 900 )); then
        echo "timed out waiting for Android curl build lock" >&2
        exit 1
    fi
    sleep 1
done
partial=""
download_lock=""
cleanup() {
    if [[ -n "$partial" ]]; then rm -f "$partial"; fi
    if [[ -n "$download_lock" ]]; then
        rmdir "$download_lock" 2>/dev/null || true
    fi
    rmdir "$lock" 2>/dev/null || true
}
trap cleanup EXIT

# A parallel ABI build may have completed while this invocation waited.
if [[ -f "$stamp" && "$(<"$stamp")" == "$stamp_value" &&
      -f "$install_root/include/curl/curl.h" &&
      -f "$install_root/lib/libcurl.a" ]]; then
    echo "curl ${CURL_VERSION} for $abi/API $api_level is current"
    exit 0
fi

downloads="$output_root/downloads"
archive="$downloads/curl-${CURL_VERSION}.tar.gz"
mkdir -p "$downloads"

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

if [[ ! -f "$archive" || "$(sha256_file "$archive")" != "$CURL_SHA256" ]]; then
    download_lock="$output_root/.curl-download-lock"
    attempt=0
    until mkdir "$download_lock" 2>/dev/null; do
        attempt=$((attempt + 1))
        if (( attempt > 300 )); then
            echo "timed out waiting for Android curl download lock" >&2
            exit 1
        fi
        sleep 1
    done
fi

# Recheck after taking the shared archive lock: another ABI may have fetched it.
if [[ -n "$download_lock" &&
      (! -f "$archive" || "$(sha256_file "$archive")" != "$CURL_SHA256") ]]; then
    partial="$archive.part.$$"
    curl --fail --location --silent --show-error --retry 3 \
        --output "$partial" "$CURL_URL"
    actual_sha256="$(sha256_file "$partial")"
    if [[ "$actual_sha256" != "$CURL_SHA256" ]]; then
        echo "curl archive checksum mismatch: $actual_sha256" >&2
        exit 1
    fi
    mv "$partial" "$archive"
    partial=""
fi
if [[ -n "$download_lock" ]]; then
    rmdir "$download_lock"
    download_lock=""
fi

build_root="$output_root/build/curl-${CURL_VERSION}-api${api_level}/$abi"
source_root="$build_root/source"
cmake_root="$build_root/cmake"
install_partial="$install_root.part.$$"
rm -rf "$build_root" "$install_partial"
mkdir -p "$source_root" "$cmake_root" "$install_partial"
tar -xzf "$archive" --strip-components=1 -C "$source_root"

jobs=4
if command -v nproc >/dev/null 2>&1; then
    jobs="$(nproc)"
elif command -v sysctl >/dev/null 2>&1; then
    jobs="$(sysctl -n hw.logicalcpu 2>/dev/null || echo 4)"
fi

echo "Building curl ${CURL_VERSION} for $abi/API $api_level"
cmake -S "$source_root" -B "$cmake_root" -G Ninja \
    -DCMAKE_BUILD_TYPE=Release \
    -DCMAKE_TOOLCHAIN_FILE="$android_ndk_root/build/cmake/android.toolchain.cmake" \
    -DANDROID_ABI="$abi" \
    -DANDROID_PLATFORM="android-${api_level}" \
    -DCMAKE_INSTALL_PREFIX="$install_partial" \
    -DBUILD_CURL_EXE=OFF \
    -DBUILD_LIBCURL_DOCS=OFF \
    -DBUILD_MISC_DOCS=OFF \
    -DENABLE_CURL_MANUAL=OFF \
    -DBUILD_SHARED_LIBS=OFF \
    -DBUILD_STATIC_LIBS=ON \
    -DBUILD_TESTING=OFF \
    -DBUILD_EXAMPLES=OFF \
    -DHTTP_ONLY=ON \
    -DCURL_USE_OPENSSL=ON \
    -DOPENSSL_ROOT_DIR="$openssl_root" \
    -DOPENSSL_INCLUDE_DIR="$openssl_root/include" \
    -DOPENSSL_SSL_LIBRARY="$openssl_root/lib/libssl.a" \
    -DOPENSSL_CRYPTO_LIBRARY="$openssl_root/lib/libcrypto.a" \
    -DOPENSSL_USE_STATIC_LIBS=ON \
    -DCURL_ZLIB=OFF \
    -DCURL_BROTLI=OFF \
    -DCURL_ZSTD=OFF \
    -DCURL_USE_LIBPSL=OFF \
    -DUSE_LIBIDN2=OFF \
    -DUSE_NGHTTP2=OFF \
    -DCURL_USE_LIBSSH=OFF \
    -DCURL_USE_LIBSSH2=OFF \
    -DCURL_USE_GSSAPI=OFF \
    -DCURL_CA_BUNDLE=none \
    -DCURL_CA_PATH=/system/etc/security/cacerts \
    -DPICKY_COMPILER=OFF
cmake --build "$cmake_root" --parallel "$jobs"
cmake --install "$cmake_root"

test -f "$install_partial/include/curl/curl.h"
test -f "$install_partial/lib/libcurl.a"
printf '%s\n' "$stamp_value" >"$install_partial/.clambhook-curl-build"
rm -rf "$install_root"
mv "$install_partial" "$install_root"
rm -rf "$build_root"
echo "Installed curl ${CURL_VERSION} for $abi/API $api_level"
