package vless

import (
	"errors"
	"fmt"
	"net"

	"github.com/clambhook/clambhook/internal/protocol"
	"github.com/google/uuid"
)

type config struct {
	uuid       uuid.UUID
	flow       string // "" or "none" for v1; other flows are rejected
	sni        string
	alpn       []string
	skipVerify bool
}

// parseConfig extracts and validates VLESS-specific settings from the shared
// Server struct. Mirrors the ok-form of type assertions used in trojan's
// parseConfig — missing required fields return an error, optional fields
// fall through to their zero value.
func parseConfig(s protocol.Server) (config, error) {
	var c config

	raw, _ := s.Settings["uuid"].(string)
	if raw == "" {
		return c, errors.New("vless: uuid is required")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return c, fmt.Errorf("vless: parse uuid %q: %w", raw, err)
	}
	c.uuid = id

	// Flow defaults to "none" (no XTLS vision / xudp). Anything else — in
	// particular "xtls-rprx-vision" — requires server-side handshake pacing
	// that's out of scope for v1; reject loudly so a misconfigured profile
	// doesn't silently fall back to plain VLESS and expose a TLS-in-TLS
	// fingerprint the user was expecting to hide.
	flow, _ := s.Settings["flow"].(string)
	switch flow {
	case "", "none":
		c.flow = "none"
	default:
		return c, fmt.Errorf("vless: flow %q not supported in v1 (only \"\" or \"none\")", flow)
	}

	if sni, ok := s.Settings["sni"].(string); ok && sni != "" {
		c.sni = sni
	} else {
		host, _, err := net.SplitHostPort(s.Address)
		if err != nil {
			return c, fmt.Errorf("vless: invalid server address %q: %w", s.Address, err)
		}
		c.sni = host
	}

	if raw, ok := s.Settings["alpn"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && s != "" {
				c.alpn = append(c.alpn, s)
			}
		}
	}

	if v, ok := s.Settings["skip_cert_verify"].(bool); ok {
		c.skipVerify = v
	}

	return c, nil
}
