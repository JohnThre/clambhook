package vmess

import (
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/protocol"
)

const testUUID = "b831381d-6324-4d53-ad4f-8cda48b30811"

func TestParseConfigMissingUUID(t *testing.T) {
	_, err := parseConfig(protocol.Server{Address: "example.com:443", Settings: map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "uuid") {
		t.Fatalf("expected uuid error, got %v", err)
	}
}

func TestParseConfigDefaultSecurity(t *testing.T) {
	cfg, err := parseConfig(protocol.Server{
		Address:  "example.com:443",
		Settings: map[string]any{"uuid": testUUID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.security != securityAES128GCM {
		t.Fatalf("default security = %#x, want %#x", cfg.security, securityAES128GCM)
	}
	if !cfg.useTLS {
		t.Error("expected useTLS=true by default")
	}
	if cfg.packetEncoding != packetEncodingAuto {
		t.Fatalf("default packetEncoding = %q, want auto", cfg.packetEncoding)
	}
}

func TestParseConfigChaCha20(t *testing.T) {
	cfg, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"uuid":     testUUID,
			"security": "chacha20-poly1305",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.security != securityChaCha20Poly1305 {
		t.Fatalf("security = %#x, want %#x", cfg.security, securityChaCha20Poly1305)
	}
}

func TestParseConfigRejectsLegacy(t *testing.T) {
	// Legacy CFB mode is pre-AEAD and cryptographically broken. Every
	// modern VMess deployment drops it; silently accepting it here would
	// give users a false sense of "it's a VMess server, it's encrypted".
	_, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"uuid":     testUUID,
			"security": "aes-128-cfb",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("expected legacy error, got %v", err)
	}
}

func TestParseConfigRejectsPlaintext(t *testing.T) {
	_, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"uuid":     testUUID,
			"security": "none",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "plaintext") {
		t.Fatalf("expected plaintext error, got %v", err)
	}
}

func TestParseConfigRejectsAlterID(t *testing.T) {
	_, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"uuid":     testUUID,
			"alter_id": int64(16),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "alter_id") {
		t.Fatalf("expected alter_id error, got %v", err)
	}
}

func TestParseConfigPacketEncodingXUDP(t *testing.T) {
	cfg, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"uuid":            testUUID,
			"packet_encoding": "xudp",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.packetEncoding != packetEncodingXUDP {
		t.Fatalf("packetEncoding = %q, want xudp", cfg.packetEncoding)
	}
}

func TestParseConfigRejectsUnknownPacketEncoding(t *testing.T) {
	_, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"uuid":            testUUID,
			"packet_encoding": "packetaddr",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "packet_encoding") {
		t.Fatalf("expected packet_encoding error, got %v", err)
	}
}
