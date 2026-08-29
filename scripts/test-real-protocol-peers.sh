#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Provision real loopback WireGuard and OpenVPN 2.6 peers on GNU/Linux, then
# run the opt-in native interoperability cases against them. CI sets
# CLAMBHOOK_REQUIRE_REAL_PEERS=1 so missing privileges or tools are failures.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_BINARY="${1:-$ROOT_DIR/build-native-sanitize/clambhook-native-tests}"
REQUIRE_PEERS="${CLAMBHOOK_REQUIRE_REAL_PEERS:-0}"

skip_or_fail() {
    local message="$1"
    if [[ "$REQUIRE_PEERS" == "1" ]]; then
        echo "protocol peers: $message" >&2
        exit 2
    fi
    echo "protocol peers: skip: $message" >&2
    exit 0
}

[[ "$(uname -s)" == "Linux" ]] || skip_or_fail "GNU/Linux is required"
[[ -x "$TEST_BINARY" ]] || skip_or_fail "native test binary is missing: $TEST_BINARY"
for tool in ip wg openvpn openssl socat sudo; do
    command -v "$tool" >/dev/null 2>&1 || skip_or_fail "$tool is required"
done
sudo -n true >/dev/null 2>&1 || skip_or_fail "passwordless test-only sudo is required"
[[ -c /dev/net/tun ]] || skip_or_fail "/dev/net/tun is unavailable"

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-protocol-peers.XXXXXX")"
WG_INTERFACE="chwg$$"
WG_TCP_PID=""
WG_UDP_PID=""
OVPN_PID_FILE="$WORK_DIR/openvpn.pid"

cleanup() {
    set +e
    if [[ -n "$WG_TCP_PID" ]]; then kill "$WG_TCP_PID" 2>/dev/null; fi
    if [[ -n "$WG_UDP_PID" ]]; then kill "$WG_UDP_PID" 2>/dev/null; fi
    if [[ -f "$OVPN_PID_FILE" ]]; then
        sudo kill "$(sudo cat "$OVPN_PID_FILE")" 2>/dev/null
    fi
    sudo ip link delete "$WG_INTERFACE" 2>/dev/null
    sudo rm -rf "$WORK_DIR"
}
trap cleanup EXIT INT TERM

WG_SERVER_PRIVATE="$(wg genkey)"
WG_SERVER_PUBLIC="$(printf '%s' "$WG_SERVER_PRIVATE" | wg pubkey)"
WG_CLIENT_PRIVATE="$(wg genkey)"
WG_CLIENT_PUBLIC="$(printf '%s' "$WG_CLIENT_PRIVATE" | wg pubkey)"
WG_SERVER_KEY="$WORK_DIR/wireguard-server.key"
install -m 0600 /dev/null "$WG_SERVER_KEY"
printf '%s\n' "$WG_SERVER_PRIVATE" > "$WG_SERVER_KEY"

sudo ip link add dev "$WG_INTERFACE" type wireguard
sudo wg set "$WG_INTERFACE" private-key "$WG_SERVER_KEY" listen-port 0 \
    peer "$WG_CLIENT_PUBLIC" allowed-ips 10.0.0.2/32
sudo ip address add 10.0.0.1/24 dev "$WG_INTERFACE"
sudo ip link set up dev "$WG_INTERFACE"
WG_PORT="$(sudo wg show "$WG_INTERFACE" listen-port)"
[[ "$WG_PORT" =~ ^[0-9]+$ && "$WG_PORT" -gt 0 ]] || {
    echo "protocol peers: WireGuard did not allocate a UDP port" >&2
    exit 1
}

socat TCP4-LISTEN:9000,bind=10.0.0.1,reuseaddr,fork EXEC:/bin/cat \
    >"$WORK_DIR/wireguard-tcp.log" 2>&1 &
WG_TCP_PID=$!
socat UDP4-RECVFROM:9001,bind=10.0.0.1,reuseaddr,fork EXEC:/bin/cat \
    >"$WORK_DIR/wireguard-udp.log" 2>&1 &
WG_UDP_PID=$!

CA_KEY="$WORK_DIR/ca.key"
CA_CERT="$WORK_DIR/ca.crt"
SERVER_KEY="$WORK_DIR/server.key"
SERVER_CSR="$WORK_DIR/server.csr"
SERVER_CERT="$WORK_DIR/server.crt"
CLIENT_KEY="$WORK_DIR/client.key"
CLIENT_CSR="$WORK_DIR/client.csr"
CLIENT_CERT="$WORK_DIR/client.crt"
SERVER_EXT="$WORK_DIR/server.ext"
CLIENT_EXT="$WORK_DIR/client.ext"

openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 1 \
    -subj '/CN=ClambHook peer test CA' -keyout "$CA_KEY" -out "$CA_CERT" \
    >/dev/null 2>&1
openssl req -newkey rsa:2048 -sha256 -nodes -subj '/CN=localhost' \
    -keyout "$SERVER_KEY" -out "$SERVER_CSR" >/dev/null 2>&1
printf '%s\n' \
    'basicConstraints=critical,CA:FALSE' \
    'keyUsage=critical,digitalSignature,keyEncipherment' \
    'extendedKeyUsage=serverAuth' \
    'subjectAltName=DNS:localhost,IP:127.0.0.1' > "$SERVER_EXT"
openssl x509 -req -sha256 -days 1 -in "$SERVER_CSR" \
    -CA "$CA_CERT" -CAkey "$CA_KEY" -set_serial 2 \
    -extfile "$SERVER_EXT" -out "$SERVER_CERT" >/dev/null 2>&1
openssl req -newkey rsa:2048 -sha256 -nodes -subj '/CN=clambhook-client' \
    -keyout "$CLIENT_KEY" -out "$CLIENT_CSR" >/dev/null 2>&1
printf '%s\n' \
    'basicConstraints=critical,CA:FALSE' \
    'keyUsage=critical,digitalSignature,keyEncipherment' \
    'extendedKeyUsage=clientAuth' > "$CLIENT_EXT"
openssl x509 -req -sha256 -days 1 -in "$CLIENT_CSR" \
    -CA "$CA_CERT" -CAkey "$CA_KEY" -set_serial 3 \
    -extfile "$CLIENT_EXT" -out "$CLIENT_CERT" >/dev/null 2>&1
chmod 0600 "$CA_KEY" "$SERVER_KEY" "$CLIENT_KEY"

OVPN_PORT=$((24000 + ($$ % 12000)))
OVPN_CONFIG="$WORK_DIR/server.conf"
printf '%s\n' \
    'dev tun' \
    'topology subnet' \
    'proto udp4' \
    'local 127.0.0.1' \
    "port $OVPN_PORT" \
    'server 10.8.0.0 255.255.255.0' \
    "ca $CA_CERT" \
    "cert $SERVER_CERT" \
    "key $SERVER_KEY" \
    'dh none' \
    'tls-server' \
    'tls-version-min 1.2' \
    'tls-ekm' \
    'remote-cert-tls client' \
    'verify-client-cert require' \
    'auth SHA256' \
    'cipher AES-256-GCM' \
    'data-ciphers AES-256-GCM:CHACHA20-POLY1305' \
    'allow-compression no' \
    'push "tun-mtu 1500"' \
    'keepalive 5 30' \
    'persist-key' \
    'persist-tun' \
    'explicit-exit-notify 1' \
    'verb 4' > "$OVPN_CONFIG"

sudo openvpn --config "$OVPN_CONFIG" --daemon \
    --writepid "$OVPN_PID_FILE" --log "$WORK_DIR/openvpn.log"
for _ in {1..100}; do
    if sudo grep -q 'Initialization Sequence Completed' "$WORK_DIR/openvpn.log"; then
        break
    fi
    if sudo grep -Eq 'Options error|Exiting due to fatal error|ERROR:' \
            "$WORK_DIR/openvpn.log"; then
        sudo cat "$WORK_DIR/openvpn.log" >&2
        exit 1
    fi
    sleep 0.1
done
sudo grep -q 'Initialization Sequence Completed' "$WORK_DIR/openvpn.log" || {
    sudo cat "$WORK_DIR/openvpn.log" >&2
    echo "protocol peers: OpenVPN peer did not become ready" >&2
    exit 1
}

env \
    CLAMBHOOK_REQUIRE_REAL_PEERS=1 \
    CLAMBHOOK_TEST_GROUP=wireguard \
    CLAMBHOOK_WG_ENDPOINT="127.0.0.1:$WG_PORT" \
    CLAMBHOOK_WG_CLIENT_PRIVATE="$WG_CLIENT_PRIVATE" \
    CLAMBHOOK_WG_SERVER_PUBLIC="$WG_SERVER_PUBLIC" \
    "$TEST_BINARY"

env \
    CLAMBHOOK_REQUIRE_REAL_PEERS=1 \
    CLAMBHOOK_TEST_GROUP=openvpn \
    CLAMBHOOK_OVPN_ENDPOINT="127.0.0.1:$OVPN_PORT" \
    CLAMBHOOK_OVPN_CA="$CA_CERT" \
    CLAMBHOOK_OVPN_CERT="$CLIENT_CERT" \
    CLAMBHOOK_OVPN_KEY="$CLIENT_KEY" \
    CLAMBHOOK_OVPN_CIPHER=AES-256-GCM \
    "$TEST_BINARY"

echo "protocol peers: real WireGuard TCP/UDP and OpenVPN UDP/TLS-EKM checks passed"
