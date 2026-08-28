// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package shadowtls

import (
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/protocol"
)

func TestParseConfig(t *testing.T) {
	t.Run("requires password", func(t *testing.T) {
		_, err := parseConfig(protocol.Server{Address: "host:443"})
		if err == nil || !strings.Contains(err.Error(), "password is required") {
			t.Fatalf("err = %v, want password is required", err)
		}
	})

	t.Run("defaults sni to address host", func(t *testing.T) {
		c, err := parseConfig(protocol.Server{
			Address:  "stls.example.invalid:443",
			Settings: map[string]any{"password": "secret"},
		})
		if err != nil {
			t.Fatalf("parseConfig: %v", err)
		}
		if c.sni != "stls.example.invalid" {
			t.Errorf("sni = %q, want stls.example.invalid", c.sni)
		}
	})

	t.Run("explicit sni and alpn", func(t *testing.T) {
		c, err := parseConfig(protocol.Server{
			Address: "1.2.3.4:443",
			Settings: map[string]any{
				"password": "secret",
				"sni":      "www.microsoft.com",
				"alpn":     []any{"h2", "http/1.1"},
			},
		})
		if err != nil {
			t.Fatalf("parseConfig: %v", err)
		}
		if c.sni != "www.microsoft.com" {
			t.Errorf("sni = %q", c.sni)
		}
		if len(c.alpn) != 2 || c.alpn[0] != "h2" || c.alpn[1] != "http/1.1" {
			t.Errorf("alpn = %#v", c.alpn)
		}
	})

	t.Run("invalid address without sni", func(t *testing.T) {
		_, err := parseConfig(protocol.Server{
			Address:  "no-port",
			Settings: map[string]any{"password": "secret"},
		})
		if err == nil || !strings.Contains(err.Error(), "invalid server address") {
			t.Fatalf("err = %v, want invalid server address", err)
		}
	})
}

func TestCheckVersion(t *testing.T) {
	ok := []map[string]any{
		nil,
		{"version": 3},
		{"version": int64(3)},
		{"version": float64(3)},
		{"version": "3"},
		{"version": ""},
	}
	for _, s := range ok {
		if err := checkVersion(s); err != nil {
			t.Errorf("checkVersion(%v) = %v, want nil", s, err)
		}
	}

	bad := []map[string]any{
		{"version": 2},
		{"version": "1"},
		{"version": float64(2)},
		{"version": true},
	}
	for _, s := range bad {
		if err := checkVersion(s); err == nil {
			t.Errorf("checkVersion(%v) = nil, want error", s)
		}
	}
}

func TestDialerRegistered(t *testing.T) {
	d, err := protocol.NewDialer(protocol.Server{
		Name:     "stls",
		Address:  "host:443",
		Protocol: "shadowtls",
		Settings: map[string]any{"password": "secret"},
	})
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	if d.Protocol() != "shadowtls" {
		t.Errorf("Protocol() = %q", d.Protocol())
	}
	caps := protocol.CapabilitiesForProtocol("shadowtls")
	if !caps.TCP || !caps.UDP || caps.UDPMode != protocol.UDPModeStream {
		t.Errorf("capabilities = %#v", caps)
	}
}
