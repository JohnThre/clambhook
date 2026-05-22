package reality

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/JohnThre/clambhook/internal/protocol"
)

// Options carries the validated, handshake-ready settings for a Reality
// client. All fields are pre-parsed so the handshake path can run without
// any further string munging or error handling.
type Options struct {
	// PublicKey is the server's static X25519 public key.
	PublicKey [32]byte

	// ShortID is zero-padded to 8 bytes on parse. A shorter or empty
	// configured short_id is a valid configuration — xray right-pads it
	// to 8 bytes before stuffing the session_id.
	ShortID [8]byte

	// ServerName is the SNI of the decoy site (e.g. "www.microsoft.com").
	// Must match the server's configured "dest"; otherwise the server
	// cannot route failed auth attempts convincingly.
	ServerName string

	// Fingerprint selects a uTLS ClientHelloID. Resolved in handshake.go;
	// validated here so a typo fails at config load, not connect time.
	Fingerprint string

	// ALPN is forwarded into the uTLS config. Typically ["h2","http/1.1"]
	// to match the decoy site; leaving it empty is fine for most servers.
	ALPN []string

	// SkipVerify disables clambhook-layer cert verification. Note that
	// Reality unconditionally sets utls.Config.InsecureSkipVerify=true
	// internally because Reality supplies its own HMAC-SHA512 verifier
	// against the server's ed25519 cert. This flag only disables THAT
	// check — use it for interop testing, never in production.
	SkipVerify bool
}

// ParseOptions extracts validated Reality Options from a protocol.Server's
// Settings map. Exported so VLESS can feed its nested [settings.reality]
// TOML block through the same parser — keeps the validation rules
// single-sourced and out of VLESS's concern.
func ParseOptions(s protocol.Server) (Options, error) {
	var opts Options

	pkRaw, _ := s.Settings["public_key"].(string)
	if pkRaw == "" {
		return opts, errors.New("reality: public_key is required")
	}
	pk, err := decodeX25519Key(pkRaw)
	if err != nil {
		return opts, fmt.Errorf("reality: public_key: %w", err)
	}
	opts.PublicKey = pk

	sidRaw, _ := s.Settings["short_id"].(string)
	sid, err := decodeShortID(sidRaw)
	if err != nil {
		return opts, fmt.Errorf("reality: short_id: %w", err)
	}
	opts.ShortID = sid

	if name, ok := s.Settings["server_name"].(string); ok && name != "" {
		opts.ServerName = name
	} else {
		host, _, err := net.SplitHostPort(s.Address)
		if err != nil {
			return opts, fmt.Errorf("reality: invalid server address %q: %w", s.Address, err)
		}
		opts.ServerName = host
	}

	opts.Fingerprint, _ = s.Settings["fingerprint"].(string)
	if _, err := resolveFingerprint(opts.Fingerprint); err != nil {
		return opts, fmt.Errorf("reality: fingerprint: %w", err)
	}

	if raw, ok := s.Settings["alpn"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && s != "" {
				opts.ALPN = append(opts.ALPN, s)
			}
		}
	}

	if v, ok := s.Settings["skip_cert_verify"].(bool); ok {
		opts.SkipVerify = v
	}

	return opts, nil
}

// decodeX25519Key accepts a 32-byte X25519 public key encoded as hex
// (64 chars) or base64-url (43 chars, unpadded; also tolerates padded).
// Mirrors xray-core's acceptance of both encodings so users can paste
// either the `x25519 -i` hex output or the `x25519` base64 output.
func decodeX25519Key(s string) ([32]byte, error) {
	var out [32]byte
	s = strings.TrimSpace(s)
	if s == "" {
		return out, errors.New("empty")
	}

	if b, err := hex.DecodeString(s); err == nil {
		if len(b) != 32 {
			return out, fmt.Errorf("hex: want 32 bytes, got %d", len(b))
		}
		copy(out[:], b)
		return out, nil
	}

	// base64-url, padded or unpadded
	for _, enc := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			if len(b) != 32 {
				return out, fmt.Errorf("base64: want 32 bytes, got %d", len(b))
			}
			copy(out[:], b)
			return out, nil
		}
	}
	return out, errors.New("expected hex (64 chars) or base64-url (32 bytes)")
}

// decodeShortID parses the short_id as hex. An empty string is a valid
// configuration — the server may accept ""-registered clients. The
// returned 8-byte array is right-zero-padded from whatever length was
// configured (0..8 bytes).
func decodeShortID(s string) ([8]byte, error) {
	var out [8]byte
	s = strings.TrimSpace(s)
	if s == "" {
		return out, nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return out, fmt.Errorf("hex: %w", err)
	}
	if len(b) > 8 {
		return out, fmt.Errorf("too long: want ≤ 8 bytes, got %d", len(b))
	}
	copy(out[:], b)
	return out, nil
}
