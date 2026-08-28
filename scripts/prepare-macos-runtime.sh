#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DAEMON="$ROOT_DIR/bin/clambhook"
SODIUM_NAME="libsodium.26.dylib"
SODIUM_LIBDIR="$(pkg-config --variable=libdir libsodium)"
SODIUM_SOURCE="$SODIUM_LIBDIR/$SODIUM_NAME"
SODIUM_DEST="$ROOT_DIR/bin/$SODIUM_NAME"
SODIUM_BUNDLE_PATH="@executable_path/../Frameworks/$SODIUM_NAME"
OPENSSL_LIBDIR="$(pkg-config --variable=libdir openssl)"
SSL_NAME="libssl.3.dylib"
CRYPTO_NAME="libcrypto.3.dylib"
SSL_SOURCE="$OPENSSL_LIBDIR/$SSL_NAME"
CRYPTO_SOURCE="$OPENSSL_LIBDIR/$CRYPTO_NAME"
SSL_DEST="$ROOT_DIR/bin/$SSL_NAME"
CRYPTO_DEST="$ROOT_DIR/bin/$CRYPTO_NAME"
SSL_BUNDLE_PATH="@executable_path/../Frameworks/$SSL_NAME"
CRYPTO_BUNDLE_PATH="@executable_path/../Frameworks/$CRYPTO_NAME"

if [[ ! -x "$DAEMON" ]]; then
    echo "missing daemon executable at $DAEMON" >&2
    exit 1
fi

mkdir -p "$ROOT_DIR/bin"

prepare_arm64_dylib() {
    local source="$1"
    local destination="$2"
    local label="$3"
    local archs

    if [[ ! -f "$source" ]]; then
        echo "missing $label dylib at $source" >&2
        exit 1
    fi
    archs="$(lipo -archs "$source" 2>/dev/null || true)"
    if [[ " $archs " != *" arm64 "* ]]; then
        echo "$label must contain an arm64 slice for Apple Silicon-only macOS builds; found: ${archs:-unknown}" >&2
        exit 1
    fi
    if [[ " $archs " == *" x86_64 "* || " $archs " == *" i386 "* ]]; then
        lipo "$source" -thin arm64 -output "$destination"
    else
        cp "$source" "$destination"
    fi
    chmod 755 "$destination"
}

daemon_archs="$(lipo -archs "$DAEMON" 2>/dev/null || true)"
if [[ " $daemon_archs " != *" arm64 "* ]]; then
    echo "daemon must contain an arm64 slice for Apple Silicon-only macOS builds; found: ${daemon_archs:-unknown}" >&2
    exit 1
fi
if [[ " $daemon_archs " == *" x86_64 "* || " $daemon_archs " == *" i386 "* ]]; then
    echo "daemon must not contain Intel slices for Apple Silicon-only macOS builds; found: $daemon_archs" >&2
    exit 1
fi

prepare_arm64_dylib "$SODIUM_SOURCE" "$SODIUM_DEST" "libsodium"
prepare_arm64_dylib "$SSL_SOURCE" "$SSL_DEST" "OpenSSL libssl"
prepare_arm64_dylib "$CRYPTO_SOURCE" "$CRYPTO_DEST" "OpenSSL libcrypto"

install_name_tool -id "@rpath/$SSL_NAME" "$SSL_DEST"
install_name_tool -id "@rpath/$CRYPTO_NAME" "$CRYPTO_DEST"
ssl_crypto_path="$(otool -L "$SSL_DEST" | awk '/libcrypto\.3\.dylib/ { print $1; exit }')"
if [[ -z "$ssl_crypto_path" ]]; then
    echo "OpenSSL libssl does not link against $CRYPTO_NAME" >&2
    exit 1
fi
if [[ "$ssl_crypto_path" != "@loader_path/$CRYPTO_NAME" ]]; then
    install_name_tool -change "$ssl_crypto_path" "@loader_path/$CRYPTO_NAME" "$SSL_DEST"
fi

current_sodium_path="$(otool -L "$DAEMON" | awk '/libsodium/ { print $1; exit }')"
if [[ -z "$current_sodium_path" ]]; then
    echo "daemon does not link against libsodium" >&2
    exit 1
fi

if [[ "$current_sodium_path" != "$SODIUM_BUNDLE_PATH" ]]; then
    install_name_tool -change "$current_sodium_path" "$SODIUM_BUNDLE_PATH" "$DAEMON"
fi

current_ssl_path="$(otool -L "$DAEMON" | awk '/libssl\.3\.dylib/ { print $1; exit }')"
current_crypto_path="$(otool -L "$DAEMON" | awk '/libcrypto\.3\.dylib/ { print $1; exit }')"
if [[ -n "$current_ssl_path" && "$current_ssl_path" != "$SSL_BUNDLE_PATH" ]]; then
    install_name_tool -change "$current_ssl_path" "$SSL_BUNDLE_PATH" "$DAEMON"
fi
if [[ -n "$current_crypto_path" && "$current_crypto_path" != "$CRYPTO_BUNDLE_PATH" ]]; then
    install_name_tool -change "$current_crypto_path" "$CRYPTO_BUNDLE_PATH" "$DAEMON"
fi

if otool -L "$DAEMON" | grep -q '/opt/homebrew'; then
    echo "daemon still contains a Homebrew runtime dependency" >&2
    otool -L "$DAEMON" >&2
    exit 1
fi

# The terminal UI is a pure-Go API client; it ships alongside the daemon in
# Contents/MacOS. Validate the Apple Silicon-only slice and, defensively,
# repoint any libsodium linkage at the bundled dylib so the executable never
# depends on a Homebrew runtime.
TUI="$ROOT_DIR/bin/clambhook-tui"
if [[ ! -x "$TUI" ]]; then
    echo "missing tui executable at $TUI" >&2
    exit 1
fi

tui_archs="$(lipo -archs "$TUI" 2>/dev/null || true)"
if [[ " $tui_archs " != *" arm64 "* ]]; then
    echo "tui must contain an arm64 slice for Apple Silicon-only macOS builds; found: ${tui_archs:-unknown}" >&2
    exit 1
fi
if [[ " $tui_archs " == *" x86_64 "* || " $tui_archs " == *" i386 "* ]]; then
    echo "tui must not contain Intel slices for Apple Silicon-only macOS builds; found: $tui_archs" >&2
    exit 1
fi

tui_sodium_path="$(otool -L "$TUI" | awk '/libsodium/ { print $1; exit }')"
if [[ -n "$tui_sodium_path" && "$tui_sodium_path" != "$SODIUM_BUNDLE_PATH" ]]; then
    install_name_tool -change "$tui_sodium_path" "$SODIUM_BUNDLE_PATH" "$TUI"
fi

if otool -L "$TUI" | grep -q '/opt/homebrew'; then
    echo "tui still contains a Homebrew runtime dependency" >&2
    otool -L "$TUI" >&2
    exit 1
fi
