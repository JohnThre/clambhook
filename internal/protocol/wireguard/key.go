package wireguard

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// wgKeyLen is the fixed size of every WireGuard Curve25519 key (private,
// public, or preshared). The spec defines nothing shorter or longer — a key
// of any other length is malformed.
const wgKeyLen = 32

// keyToHex decodes a base64-encoded 32-byte WireGuard key and returns it as
// lowercase hex. WireGuard's UAPI (IpcSet) wire format requires hex; the
// wg(8) tool accepts base64 for human convenience. We accept base64 at the
// TOML boundary and translate once so the device layer doesn't care.
func keyToHex(b64 string) (string, error) {
	if b64 == "" {
		return "", errors.New("wireguard: empty key")
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("wireguard: decode key: %w", err)
	}
	if len(raw) != wgKeyLen {
		return "", fmt.Errorf("wireguard: key must be %d bytes, got %d", wgKeyLen, len(raw))
	}
	return hex.EncodeToString(raw), nil
}
