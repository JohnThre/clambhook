package vless

import (
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/protocol"
)

func TestParseConfigMissingUUID(t *testing.T) {
	_, err := parseConfig(protocol.Server{
		Address:  "example.com:443",
		Settings: map[string]any{},
	})
	if err == nil || !strings.Contains(err.Error(), "uuid") {
		t.Fatalf("expected uuid-required error, got %v", err)
	}
}

func TestParseConfigBadUUID(t *testing.T) {
	_, err := parseConfig(protocol.Server{
		Address:  "example.com:443",
		Settings: map[string]any{"uuid": "not-a-uuid"},
	})
	if err == nil || !strings.Contains(err.Error(), "parse uuid") {
		t.Fatalf("expected parse-uuid error, got %v", err)
	}
}

func TestParseConfigSNIDefaultsToHost(t *testing.T) {
	cfg, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"uuid": "b831381d-6324-4d53-ad4f-8cda48b30811",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.sni != "example.com" {
		t.Fatalf("sni = %q, want example.com", cfg.sni)
	}
	if cfg.flow != "none" {
		t.Fatalf("flow = %q, want none", cfg.flow)
	}
}

func TestParseConfigRejectsUnsupportedFlow(t *testing.T) {
	// xtls-rprx-vision is the main deployment flow in the wild — we reject
	// it loudly so users don't think their vision is active when it isn't.
	_, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"uuid": "b831381d-6324-4d53-ad4f-8cda48b30811",
			"flow": "xtls-rprx-vision",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected unsupported-flow error, got %v", err)
	}
}

func TestParseConfigAcceptsExplicitNoneFlow(t *testing.T) {
	cfg, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"uuid": "b831381d-6324-4d53-ad4f-8cda48b30811",
			"flow": "none",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.flow != "none" {
		t.Fatalf("flow = %q, want none", cfg.flow)
	}
}

// Reality integration: VLESS parses a nested [settings.reality] block
// when security = "reality" and hands it to reality.ParseOptions. Two
// signals here — the security field lands in config, and bad nested
// blocks surface as clear vless-prefixed errors rather than raw
// reality errors bubbling up without context.

const realityPubHex = "0102030405060708091011121314151617181920212223242526272829303132"

func TestParseConfigSecurityDefaultsToTLS(t *testing.T) {
	cfg, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"uuid": "b831381d-6324-4d53-ad4f-8cda48b30811",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.security != "tls" {
		t.Fatalf("security = %q, want tls", cfg.security)
	}
}

func TestParseConfigSecurityReality(t *testing.T) {
	cfg, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"uuid":     "b831381d-6324-4d53-ad4f-8cda48b30811",
			"security": "reality",
			"reality": map[string]any{
				"public_key":  realityPubHex,
				"server_name": "www.microsoft.com",
				"short_id":    "a1b2c3d4",
				"fingerprint": "chrome",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.security != "reality" {
		t.Fatalf("security = %q, want reality", cfg.security)
	}
	if cfg.realityOpts.ServerName != "www.microsoft.com" {
		t.Fatalf("realityOpts.ServerName = %q", cfg.realityOpts.ServerName)
	}
}

func TestParseConfigSecurityRealityMissingBlock(t *testing.T) {
	_, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"uuid":     "b831381d-6324-4d53-ad4f-8cda48b30811",
			"security": "reality",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "reality") {
		t.Fatalf("expected reality-block error, got %v", err)
	}
}

func TestParseConfigSecurityRejectsUnknown(t *testing.T) {
	_, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"uuid":     "b831381d-6324-4d53-ad4f-8cda48b30811",
			"security": "quic",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "security") {
		t.Fatalf("expected unsupported security error, got %v", err)
	}
}

func TestParseConfigALPN(t *testing.T) {
	cfg, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"uuid": "b831381d-6324-4d53-ad4f-8cda48b30811",
			"alpn": []any{"h2", "http/1.1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.alpn) != 2 || cfg.alpn[0] != "h2" || cfg.alpn[1] != "http/1.1" {
		t.Fatalf("alpn = %v", cfg.alpn)
	}
}
