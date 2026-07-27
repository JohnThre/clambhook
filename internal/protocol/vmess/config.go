package vmess

import (
	"crypto/md5"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/pkg/cnet"
)

// Security methods for the VMESS body stream. Only AEAD ciphers are supported;
// legacy CFB/none and non-AEAD headers (alterId > 0) are intentionally out of
// scope.
const (
	securityAES128GCM        = "aes-128-gcm"
	securityChaCha20Poly1305 = "chacha20-poly1305"
	securityAuto             = "auto"
)

// secByte is the on-wire security selector placed in the request header.
const (
	secByteAES128GCM        = 0x03
	secByteChaCha20Poly1305 = 0x04
)

// vmessMagic is appended to the 16-byte user UUID and MD5-hashed to derive the
// command key used for header AEAD and auth-id encryption.
var vmessMagic = []byte("c48619fe-8f02-49e0-b9e9-edf763e17e21")

type config struct {
	uuid       [16]byte
	cmdKey     [16]byte
	security   string
	secByte    byte
	tls        bool
	sni        string
	alpn       []string
	skipVerify bool
	address    string
}

func parseConfig(s protocol.Server) (config, error) {
	var c config

	rawUUID, _ := s.Settings["uuid"].(string)
	if rawUUID == "" {
		return c, errors.New("vmess: uuid is required")
	}
	id, err := parseUUID(rawUUID)
	if err != nil {
		return c, err
	}
	c.uuid = id

	sum := md5.Sum(append(id[:], vmessMagic...))
	c.cmdKey = sum

	if err := checkAlterID(s.Settings); err != nil {
		return c, err
	}

	security := securityFromSettings(s.Settings)
	switch security {
	case securityAES128GCM:
		c.security = securityAES128GCM
		c.secByte = secByteAES128GCM
	case securityChaCha20Poly1305:
		c.security = securityChaCha20Poly1305
		c.secByte = secByteChaCha20Poly1305
	case securityAuto, "":
		// Prefer AES-128-GCM where hardware AES is present (constant-time and
		// fast); otherwise ChaCha20-Poly1305 avoids cache-timing risk.
		if cnet.AES256GCMAvailable() {
			c.security = securityAES128GCM
			c.secByte = secByteAES128GCM
		} else {
			c.security = securityChaCha20Poly1305
			c.secByte = secByteChaCha20Poly1305
		}
	default:
		return c, fmt.Errorf("vmess: unsupported security %q", security)
	}

	if v, ok := s.Settings["tls"].(bool); ok {
		c.tls = v
	}

	if sni, ok := s.Settings["sni"].(string); ok && sni != "" {
		c.sni = sni
	} else {
		host, _, err := net.SplitHostPort(s.Address)
		if err == nil {
			c.sni = host
		}
	}

	if raw, ok := s.Settings["alpn"].([]any); ok {
		for _, v := range raw {
			if str, ok := v.(string); ok && str != "" {
				c.alpn = append(c.alpn, str)
			}
		}
	}

	if v, ok := s.Settings["skip_cert_verify"].(bool); ok {
		c.skipVerify = v
	}

	c.address = s.Address
	return c, nil
}

func securityFromSettings(settings map[string]any) string {
	if v, ok := settings["security"].(string); ok && v != "" {
		return strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := settings["method"].(string); ok && v != "" {
		return strings.ToLower(strings.TrimSpace(v))
	}
	return ""
}

// checkAlterID rejects legacy (non-AEAD) VMESS configs. Only alterId 0 uses the
// AEAD header this package implements.
func checkAlterID(settings map[string]any) error {
	var alter int
	switch v := settings["alter_id"].(type) {
	case nil:
		return nil
	case int:
		alter = v
	case int64:
		alter = int(v)
	case float64:
		alter = int(v)
	default:
		return fmt.Errorf("vmess: alter_id must be an integer")
	}
	if alter != 0 {
		return fmt.Errorf("vmess: alter_id %d is not supported (AEAD-only, use 0)", alter)
	}
	return nil
}

// parseUUID accepts the canonical 8-4-4-4-12 hex form.
func parseUUID(s string) ([16]byte, error) {
	var out [16]byte
	clean := strings.ReplaceAll(strings.TrimSpace(s), "-", "")
	if len(clean) != 32 {
		return out, fmt.Errorf("vmess: invalid uuid %q", s)
	}
	for i := 0; i < 16; i++ {
		var b byte
		for j := 0; j < 2; j++ {
			c := clean[i*2+j]
			var nibble byte
			switch {
			case c >= '0' && c <= '9':
				nibble = c - '0'
			case c >= 'a' && c <= 'f':
				nibble = c - 'a' + 10
			case c >= 'A' && c <= 'F':
				nibble = c - 'A' + 10
			default:
				return out, fmt.Errorf("vmess: invalid uuid %q", s)
			}
			b = b<<4 | nibble
		}
		out[i] = b
	}
	return out, nil
}
