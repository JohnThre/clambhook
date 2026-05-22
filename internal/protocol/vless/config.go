package vless

import (
	"errors"
	"fmt"
	"net"

	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/internal/protocol/reality"
	"github.com/google/uuid"
)

type config struct {
	uuid       uuid.UUID
	flow       string // "" or "none" for v1; other flows are rejected
	sni        string
	alpn       []string
	skipVerify bool

	// security selects the outer transport layer.
	//   "" or "tls" — stock crypto/tls (default; existing behavior).
	//   "reality"  — XTLS Reality handshake via reality.Client.
	security string

	// realityOpts is populated only when security == "reality". The nested
	// [settings.reality] TOML block is parsed into these options so the
	// VLESS handshake can hand them to reality.Client as-is.
	realityOpts reality.Options
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

	// security is optional. Empty string is treated as "tls" (the existing
	// behavior). "reality" requires a nested [settings.reality] block; we
	// wrap parseOptions by synthesizing a protocol.Server from that block
	// so reality's own parser can run without knowing about VLESS.
	sec, _ := s.Settings["security"].(string)
	switch sec {
	case "", "tls":
		c.security = "tls"
	case "reality":
		c.security = "reality"
		nested, ok := s.Settings["reality"].(map[string]any)
		if !ok {
			return c, errors.New("vless: security = \"reality\" requires a [settings.reality] block")
		}
		// Reality has its own server_name/alpn/skip_cert_verify; the
		// VLESS-level sni / alpn / skip_cert_verify fields are ignored in
		// that mode. Feed Reality only what the nested block specifies.
		ropts, err := reality.ParseOptions(protocol.Server{
			Name:     s.Name,
			Address:  s.Address,
			Protocol: "reality",
			Settings: nested,
		})
		if err != nil {
			return c, fmt.Errorf("vless: reality block: %w", err)
		}
		c.realityOpts = ropts
	default:
		return c, fmt.Errorf("vless: security %q not supported (want \"\", \"tls\", or \"reality\")", sec)
	}

	return c, nil
}
