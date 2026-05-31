#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${1:-$ROOT_DIR/ui/apple/Frameworks/ClambhookMobile.xcframework}"

if ! command -v gomobile >/dev/null 2>&1; then
    echo "gomobile is required to build the embedded iOS runtime XCFramework." >&2
    echo "Install it with: go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init" >&2
    exit 127
fi

rm -rf "$OUT"
mkdir -p "$(dirname "$OUT")"
cd "$ROOT_DIR"

# gomobile probes golang.org/x/mobile/bind directly, which does not resolve
# from this repo's automatic vendor mode even when the package is vendored.
GOFLAGS="${GOFLAGS:+$GOFLAGS }-mod=mod" CGO_ENABLED=0 gomobile bind \
    -target=ios \
    -o "$OUT" \
    ./pkg/mobile
