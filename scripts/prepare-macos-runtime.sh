#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DAEMON="$ROOT_DIR/bin/clambhook"
SODIUM_NAME="libsodium.26.dylib"
SODIUM_LIBDIR="$(pkg-config --variable=libdir libsodium)"
SODIUM_SOURCE="$SODIUM_LIBDIR/$SODIUM_NAME"
SODIUM_DEST="$ROOT_DIR/bin/$SODIUM_NAME"
SODIUM_BUNDLE_PATH="@executable_path/../Frameworks/$SODIUM_NAME"

if [[ ! -x "$DAEMON" ]]; then
    echo "missing daemon executable at $DAEMON" >&2
    exit 1
fi

if [[ ! -f "$SODIUM_SOURCE" ]]; then
    echo "missing libsodium dylib at $SODIUM_SOURCE" >&2
    exit 1
fi

mkdir -p "$ROOT_DIR/bin"

daemon_archs="$(lipo -archs "$DAEMON" 2>/dev/null || true)"
if [[ " $daemon_archs " != *" arm64 "* ]]; then
    echo "daemon must contain an arm64 slice for Apple Silicon-only macOS builds; found: ${daemon_archs:-unknown}" >&2
    exit 1
fi
if [[ " $daemon_archs " == *" x86_64 "* || " $daemon_archs " == *" i386 "* ]]; then
    echo "daemon must not contain Intel slices for Apple Silicon-only macOS builds; found: $daemon_archs" >&2
    exit 1
fi

sodium_archs="$(lipo -archs "$SODIUM_SOURCE" 2>/dev/null || true)"
if [[ " $sodium_archs " != *" arm64 "* ]]; then
    echo "libsodium must contain an arm64 slice for Apple Silicon-only macOS builds; found: ${sodium_archs:-unknown}" >&2
    exit 1
fi
if [[ " $sodium_archs " == *" x86_64 "* || " $sodium_archs " == *" i386 "* ]]; then
    lipo "$SODIUM_SOURCE" -thin arm64 -output "$SODIUM_DEST"
else
    cp "$SODIUM_SOURCE" "$SODIUM_DEST"
fi
chmod 755 "$SODIUM_DEST"

current_sodium_path="$(otool -L "$DAEMON" | awk '/libsodium/ { print $1; exit }')"
if [[ -z "$current_sodium_path" ]]; then
    echo "daemon does not link against libsodium" >&2
    exit 1
fi

if [[ "$current_sodium_path" != "$SODIUM_BUNDLE_PATH" ]]; then
    install_name_tool -change "$current_sodium_path" "$SODIUM_BUNDLE_PATH" "$DAEMON"
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
