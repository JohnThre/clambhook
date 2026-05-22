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

CGO_ENABLED=0 gomobile bind \
    -target=android/arm,android/arm64,android/amd64 \
    -androidapi 26 \
    -javapkg=com.clambhook \
    -o "$OUT" \
    ./pkg/mobile
