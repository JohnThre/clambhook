#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="$ROOT_DIR/ui/apple/Frameworks"
OUT="$OUT_DIR/ClambhookMobile.xcframework"

if ! command -v gomobile >/dev/null 2>&1; then
    echo "gomobile is required to build the Apple mobile XCFramework." >&2
    echo "Install it with: go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init" >&2
    exit 2
fi

mkdir -p "$OUT_DIR"
rm -rf "$OUT"

export GOCACHE="${GOCACHE:-/tmp/clambhook-go-cache}"
export GOFLAGS="${GOFLAGS:--mod=mod}"
export CLANG_MODULE_CACHE_PATH="${CLANG_MODULE_CACHE_PATH:-/tmp/clambhook-clang-cache}"
export CGO_ENABLED=0

gomobile bind \
    -tags=purego \
    -target=ios,iossimulator,macos/arm64 \
    -iosversion=13.0 \
    -macosversion=14.0 \
    -o "$OUT" \
    "$ROOT_DIR/pkg/mobile"
