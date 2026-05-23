package vmess

import (
	"errors"
	"fmt"
	"net"

	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/google/uuid"
)

// VMess data-cipher (security) byte values per V2Fly spec.
const (
	securityAES128GCM        byte = 0x03
	securityChaCha20Poly1305 byte = 0x04
)

type config struct {
	uuid           uuid.UUID
	security       byte
	useTLS         bool
	sni            string
	alpn           []string
	skipVerify     bool
	packetEncoding string
}

const (
	packetEncodingAuto   = "auto"
	packetEncodingLegacy = "legacy"
	packetEncodingXUDP   = "xudp"
)

// parseConfig extracts VMess-specific settings from the shared Server struct.
// Defaults: security=aes-128-gcm, tls=true. Rejects legacy (alter_id > 0) and
// deprecated/plaintext ciphers (none, zero, aes-128-cfb) — those modes are
// cryptographically broken or non-confidential and have no legitimate use.
func parseConfig(s protocol.Server) (config, error) {
	var c config

	raw, _ := s.Settings["uuid"].(string)
	if raw == "" {
		return c, errors.New("vmess: uuid is required")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return c, fmt.Errorf("vmess: parse uuid %q: %w", raw, err)
	}
	c.uuid = id

	// alter_id > 0 selects the legacy MD5-authenticated path. We only
	// implement the AEAD path, so reject non-zero explicitly rather than
	// silently treating it as AEAD (which would not interop).
	if v, ok := s.Settings["alter_id"].(int64); ok && v != 0 {
		return c, fmt.Errorf("vmess: alter_id %d unsupported; only AEAD (alter_id=0) is implemented", v)
	}

	sec, _ := s.Settings["security"].(string)
	if sec == "" {
		sec = "aes-128-gcm"
	}
	switch sec {
	case "aes-128-gcm":
		c.security = securityAES128GCM
	case "chacha20-poly1305":
		c.security = securityChaCha20Poly1305
	case "none", "zero":
		return c, fmt.Errorf("vmess: security %q is plaintext and not supported", sec)
	case "aes-128-cfb":
		return c, fmt.Errorf("vmess: security %q is legacy pre-AEAD and not supported", sec)
	default:
		return c, fmt.Errorf("vmess: unknown security %q", sec)
	}

	// TLS defaults to on. Leaving it off is only useful for local loopback
	// testing — VMess's body AEAD gives confidentiality but TLS still matters
	// for metadata protection (e.g. SNI-level routing, DPI resistance).
	c.useTLS = true
	if v, ok := s.Settings["tls"].(bool); ok {
		c.useTLS = v
	}

	if sni, ok := s.Settings["sni"].(string); ok && sni != "" {
		c.sni = sni
	} else {
		host, _, err := net.SplitHostPort(s.Address)
		if err != nil {
			return c, fmt.Errorf("vmess: invalid server address %q: %w", s.Address, err)
		}
		c.sni = host
	}

	if rawA, ok := s.Settings["alpn"].([]any); ok {
		for _, v := range rawA {
			if s, ok := v.(string); ok && s != "" {
				c.alpn = append(c.alpn, s)
			}
		}
	}

	if v, ok := s.Settings["skip_cert_verify"].(bool); ok {
		c.skipVerify = v
	}

	c.packetEncoding = packetEncodingAuto
	if v, ok := s.Settings["packet_encoding"].(string); ok && v != "" {
		switch v {
		case packetEncodingAuto, packetEncodingLegacy, packetEncodingXUDP:
			c.packetEncoding = v
		case "none":
			c.packetEncoding = packetEncodingLegacy
		default:
			return c, fmt.Errorf("vmess: unknown packet_encoding %q (supported: auto, legacy, xudp)", v)
		}
	}

	return c, nil
}
