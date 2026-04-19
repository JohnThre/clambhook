package vless

import (
	"strings"
	"testing"

	"github.com/clambhook/clambhook/internal/protocol"
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
