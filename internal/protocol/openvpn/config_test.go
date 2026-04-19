package openvpn

import (
	"strings"
	"testing"

	"github.com/clambhook/clambhook/internal/protocol"
)

// Fixtures (CA + client cert/key PEMs) come from fixtures_test.go via
// testFixturePEMs(t). They're generated once per test binary to keep
// these cases self-contained while avoiding repeated keygen.

func TestParseConfigRequiresAddress(t *testing.T) {
	ca, cert, key := testFixturePEMs(t)
	_, err := parseConfig(protocol.Server{
		Address: "",
		Settings: map[string]any{
			"ca_cert":     ca,
			"client_cert": cert,
			"client_key":  key,
		},
	})
	if err == nil {
		t.Fatal("expected error for empty address")
	}
}

func TestParseConfigRequiresCA(t *testing.T) {
	_, cert, key := testFixturePEMs(t)
	_, err := parseConfig(protocol.Server{
		Address: "vpn.example.com:1194",
		Settings: map[string]any{
			"client_cert": cert,
			"client_key":  key,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "ca_cert is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseConfigRejectsInvalidCAPEM(t *testing.T) {
	_, cert, key := testFixturePEMs(t)
	_, err := parseConfig(protocol.Server{
		Address: "vpn.example.com:1194",
		Settings: map[string]any{
			"ca_cert":     "not a pem",
			"client_cert": cert,
			"client_key":  key,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "valid PEM") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseConfigRequiresClientCertAndKey(t *testing.T) {
	ca, cert, _ := testFixturePEMs(t)
	_, err := parseConfig(protocol.Server{
		Address: "vpn.example.com:1194",
		Settings: map[string]any{
			"ca_cert":     ca,
			"client_cert": cert,
			// missing client_key
		},
	})
	if err == nil || !strings.Contains(err.Error(), "client_cert and client_key") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseConfigHappyPath(t *testing.T) {
	ca, cert, key := testFixturePEMs(t)
	cfg, err := parseConfig(protocol.Server{
		Address: "vpn.example.com:1194",
		Settings: map[string]any{
			"ca_cert":     ca,
			"client_cert": cert,
			"client_key":  key,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.remote != "vpn.example.com:1194" {
		t.Errorf("remote = %q", cfg.remote)
	}
	if cfg.tunMTU != 1500 {
		t.Errorf("tunMTU = %d (want default 1500)", cfg.tunMTU)
	}
	if cfg.caPool == nil {
		t.Error("caPool is nil")
	}
	if len(cfg.clientCert.Certificate) == 0 {
		t.Error("clientCert is empty")
	}
}

func TestParseConfigPartialCredsRejected(t *testing.T) {
	ca, cert, key := testFixturePEMs(t)
	for _, tc := range []struct {
		name string
		set  map[string]any
	}{
		{"user only", map[string]any{"username": "alice"}},
		{"pass only", map[string]any{"password": "pw"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := map[string]any{
				"ca_cert":     ca,
				"client_cert": cert,
				"client_key":  key,
			}
			for k, v := range tc.set {
				settings[k] = v
			}
			_, err := parseConfig(protocol.Server{
				Address:  "vpn.example.com:1194",
				Settings: settings,
			})
			if err == nil || !strings.Contains(err.Error(), "username and password must be set together") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestParseConfigRejectsUnknownCipher(t *testing.T) {
	ca, cert, key := testFixturePEMs(t)
	_, err := parseConfig(protocol.Server{
		Address: "vpn.example.com:1194",
		Settings: map[string]any{
			"ca_cert":     ca,
			"client_cert": cert,
			"client_key":  key,
			"cipher":      "AES-128-CBC", // unsupported
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported cipher") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseConfigCipherNormalisedToUpper(t *testing.T) {
	ca, cert, key := testFixturePEMs(t)
	cfg, err := parseConfig(protocol.Server{
		Address: "vpn.example.com:1194",
		Settings: map[string]any{
			"ca_cert":     ca,
			"client_cert": cert,
			"client_key":  key,
			"cipher":      "aes-256-gcm",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.cipher != "AES-256-GCM" {
		t.Errorf("cipher = %q, want AES-256-GCM", cfg.cipher)
	}
}
