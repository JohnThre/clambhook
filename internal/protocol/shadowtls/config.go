package shadowtls

import (
	"fmt"
	"net"

	"github.com/JohnThre/clambhook/internal/protocol"
)

type config struct {
	password   string
	sni        string
	alpn       []string
	skipVerify bool
}

// parseConfig extracts and validates ShadowTLS-specific settings from the
// shared Server struct. Follows the same shape as trojan/vmess: ok-form type
// assertions with type-aware defaults.
//
// Recognized settings:
//
//	password         (string, required) — shared secret with the ShadowTLS server
//	sni              (string)           — handshake server name for the real TLS
//	                                       handshake; defaults to the host of
//	                                       server.Address
//	alpn             ([]string)         — ALPN presented in the TLS handshake
//	version          (int/string)       — only "3" is supported (default 3)
//	skip_cert_verify (bool)             — skip handshake-server cert verification
func parseConfig(s protocol.Server) (config, error) {
	var c config

	pw, _ := s.Settings["password"].(string)
	if pw == "" {
		return c, fmt.Errorf("shadowtls: password is required")
	}
	c.password = pw

	if err := checkVersion(s.Settings); err != nil {
		return c, err
	}

	if sni, ok := s.Settings["sni"].(string); ok && sni != "" {
		c.sni = sni
	} else {
		host, _, err := net.SplitHostPort(s.Address)
		if err != nil {
			return c, fmt.Errorf("shadowtls: invalid server address %q: %w", s.Address, err)
		}
		c.sni = host
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

	return c, nil
}

// checkVersion rejects any requested protocol version other than v3. ShadowTLS
// v1/v2 are intentionally out of scope; only strict v3 is implemented.
func checkVersion(settings map[string]any) error {
	switch v := settings["version"].(type) {
	case nil:
		return nil
	case string:
		if v == "" || v == "3" {
			return nil
		}
		return fmt.Errorf("shadowtls: version %q is not supported (v3 only)", v)
	case int:
		if v == 3 {
			return nil
		}
		return fmt.Errorf("shadowtls: version %d is not supported (v3 only)", v)
	case int64:
		if v == 3 {
			return nil
		}
		return fmt.Errorf("shadowtls: version %d is not supported (v3 only)", v)
	case float64:
		if v == 3 {
			return nil
		}
		return fmt.Errorf("shadowtls: version %v is not supported (v3 only)", v)
	default:
		return fmt.Errorf("shadowtls: version must be 3")
	}
}
