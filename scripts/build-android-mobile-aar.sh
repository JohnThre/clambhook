#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${1:-$ROOT_DIR/ui/android/app/libs/clambhookmobile.aar}"

if ! command -v gomobile >/dev/null 2>&1; then
    echo "gomobile is required to build the embedded Android daemon AAR." >&2
    echo "Install it with: go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init" >&2
    exit 127
fi

mkdir -p "$(dirname "$OUT")"
cd "$ROOT_DIR"

# gomobile synthesizes a temporary module and runs `go mod tidy`; with the
# repo's vendor/ directory that fails under the default -mod=vendor. Force
# module mode so the bind resolves dependencies from the module cache.
#
# gomobile forces CGO on for Android (JNI glue), which would drag in
# pkg/cnet's libsodium/libcnet C bindings — symbols that are never linked into
# libgojni.so and fail at dlopen. The `purego` tag selects the pure-Go crypto
# path (pkg/cnet/cnet_purego.go) so the AAR is self-contained.
GOFLAGS=-mod=mod gomobile bind \
    -target=android/arm,android/arm64,android/amd64 \
    -androidapi 30 \
    -javapkg=com.clambhook \
    -tags purego \
    -o "$OUT" \
    ./pkg/mobile
