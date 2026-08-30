#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_BINARY="${1:-$ROOT_DIR/build-native-sanitize/clambhook-native-tests}"
OUTLINE_REPOSITORY="https://github.com/OutlineFoundation/tunnel-server.git"
OUTLINE_COMMIT="4d09f750827738d21432095a46e455d24e172109"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-outline.XXXXXX")"
SERVER_PID=""
TCP_PID=""
UDP_PID=""

cleanup() {
    set +e
    [[ -z "$SERVER_PID" ]] || kill "$SERVER_PID" 2>/dev/null
    [[ -z "$TCP_PID" ]] || kill "$TCP_PID" 2>/dev/null
    [[ -z "$UDP_PID" ]] || kill "$UDP_PID" 2>/dev/null
    rm -rf "$WORK_DIR"
}
trap cleanup EXIT INT TERM

for tool in git go socat; do
    command -v "$tool" >/dev/null 2>&1 || {
        echo "Outline interoperability requires $tool" >&2
        exit 2
    }
done
[[ -x "$TEST_BINARY" ]] || {
    echo "native test binary is missing: $TEST_BINARY" >&2
    exit 2
}

git -c advice.detachedHead=false clone --filter=blob:none --no-checkout \
    "$OUTLINE_REPOSITORY" "$WORK_DIR/tunnel-server"
git -C "$WORK_DIR/tunnel-server" checkout --detach "$OUTLINE_COMMIT"
test "$(git -C "$WORK_DIR/tunnel-server" rev-parse HEAD)" = "$OUTLINE_COMMIT"
go -C "$WORK_DIR/tunnel-server" build \
    -o "$WORK_DIR/outline-ss-server" ./cmd/outline-ss-server

SERVER_PORT=$((29000 + ($$ % 1000)))
TCP_PORT=$((30000 + ($$ % 1000)))
UDP_PORT=$((31000 + ($$ % 1000)))
CONFIG="$WORK_DIR/outline.yml"
cat > "$CONFIG" <<EOF
services:
  - listeners:
      - type: tcp
        address: "127.0.0.1:${SERVER_PORT}"
      - type: udp
        address: "127.0.0.1:${SERVER_PORT}"
    keys:
      - id: aes128
        cipher: aes-128-gcm
        secret: OutlineAES128
      - id: aes256
        cipher: aes-256-gcm
        secret: OutlineAES256
      - id: chacha
        cipher: chacha20-ietf-poly1305
        secret: OutlineChaCha
EOF

socat TCP4-LISTEN:"$TCP_PORT",bind=127.0.0.1,reuseaddr,fork EXEC:/bin/cat \
    >"$WORK_DIR/tcp.log" 2>&1 &
TCP_PID=$!
socat UDP4-RECVFROM:"$UDP_PORT",bind=127.0.0.1,reuseaddr,fork EXEC:/bin/cat \
    >"$WORK_DIR/udp.log" 2>&1 &
UDP_PID=$!
"$WORK_DIR/outline-ss-server" -config "$CONFIG" \
    >"$WORK_DIR/server.log" 2>&1 &
SERVER_PID=$!

for _ in {1..50}; do
    kill -0 "$SERVER_PID" 2>/dev/null || {
        cat "$WORK_DIR/server.log" >&2
        exit 1
    }
    if grep -Eq 'service|listener|started|running' "$WORK_DIR/server.log"; then
        break
    fi
    sleep 0.1
done

env \
    CLAMBHOOK_TEST_GROUP=protocol \
    CLAMBHOOK_OUTLINE_ENDPOINT="127.0.0.1:${SERVER_PORT}" \
    CLAMBHOOK_OUTLINE_TCP_TARGET="127.0.0.1:${TCP_PORT}" \
    CLAMBHOOK_OUTLINE_UDP_TARGET="127.0.0.1:${UDP_PORT}" \
    "$TEST_BINARY"

echo "Outline interoperability: TCP, UDP, and prefixes passed for all supported ciphers"
