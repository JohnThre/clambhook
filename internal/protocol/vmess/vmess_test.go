// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package vmess

import (
	"testing"

	"github.com/JohnThre/clambhook/internal/protocol"
)

const testUUID = "b831381d-6324-4d53-ad4f-8cda48b30811"

func TestDialerRegistered(t *testing.T) {
	d, err := protocol.NewDialer(protocol.Server{
		Name:     "test",
		Address:  "example.com:443",
		Protocol: "vmess",
		Settings: map[string]any{"uuid": testUUID},
	})
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	if d.Protocol() != "vmess" {
		t.Errorf("Protocol() = %q, want vmess", d.Protocol())
	}
	if _, ok := d.(protocol.PacketDialer); !ok {
		t.Error("dialer does not implement PacketDialer")
	}
}

func TestCapabilities(t *testing.T) {
	caps := vmessCapabilities()
	if !caps.TCP || !caps.UDP {
		t.Fatalf("caps = %+v, want TCP+UDP", caps)
	}
	if caps.UDPMode != protocol.UDPModeStream {
		t.Errorf("UDPMode = %q, want %q", caps.UDPMode, protocol.UDPModeStream)
	}
}

func TestParseConfig(t *testing.T) {
	t.Run("missing uuid", func(t *testing.T) {
		_, err := parseConfig(protocol.Server{Address: "h:1", Settings: map[string]any{}})
		if err == nil {
			t.Fatal("want error for missing uuid")
		}
	})

	t.Run("invalid uuid", func(t *testing.T) {
		_, err := parseConfig(protocol.Server{Address: "h:1", Settings: map[string]any{"uuid": "nope"}})
		if err == nil {
			t.Fatal("want error for invalid uuid")
		}
	})

	t.Run("alter_id nonzero rejected", func(t *testing.T) {
		_, err := parseConfig(protocol.Server{Address: "h:1", Settings: map[string]any{
			"uuid": testUUID, "alter_id": 4,
		}})
		if err == nil {
			t.Fatal("want error for alter_id > 0")
		}
	})

	t.Run("alter_id zero ok", func(t *testing.T) {
		_, err := parseConfig(protocol.Server{Address: "h:1", Settings: map[string]any{
			"uuid": testUUID, "alter_id": 0,
		}})
		if err != nil {
			t.Fatalf("alter_id 0: %v", err)
		}
	})

	t.Run("explicit security", func(t *testing.T) {
		c, err := parseConfig(protocol.Server{Address: "h:1", Settings: map[string]any{
			"uuid": testUUID, "security": "chacha20-poly1305",
		}})
		if err != nil {
			t.Fatal(err)
		}
		if c.security != securityChaCha20Poly1305 || c.secByte != secByteChaCha20Poly1305 {
			t.Errorf("security = %q byte=%#x", c.security, c.secByte)
		}
	})

	t.Run("bad security", func(t *testing.T) {
		_, err := parseConfig(protocol.Server{Address: "h:1", Settings: map[string]any{
			"uuid": testUUID, "security": "rc4",
		}})
		if err == nil {
			t.Fatal("want error for bad security")
		}
	})

	t.Run("auto picks a supported cipher", func(t *testing.T) {
		c, err := parseConfig(protocol.Server{Address: "h:1", Settings: map[string]any{
			"uuid": testUUID,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if c.security != securityAES128GCM && c.security != securityChaCha20Poly1305 {
			t.Errorf("auto security = %q", c.security)
		}
	})

	t.Run("tls and sni", func(t *testing.T) {
		c, err := parseConfig(protocol.Server{Address: "host.example:443", Settings: map[string]any{
			"uuid": testUUID, "tls": true, "sni": "example.org",
			"alpn": []any{"h2", "http/1.1"}, "skip_cert_verify": true,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if !c.tls || c.sni != "example.org" || !c.skipVerify {
			t.Errorf("tls cfg = %+v", c)
		}
		if len(c.alpn) != 2 {
			t.Errorf("alpn = %v", c.alpn)
		}
	})

	t.Run("sni defaults to host", func(t *testing.T) {
		c, err := parseConfig(protocol.Server{Address: "host.example:443", Settings: map[string]any{
			"uuid": testUUID,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if c.sni != "host.example" {
			t.Errorf("sni = %q, want host.example", c.sni)
		}
	})
}

func TestParseUUID(t *testing.T) {
	id, err := parseUUID(testUUID)
	if err != nil {
		t.Fatal(err)
	}
	want := [16]byte{0xb8, 0x31, 0x38, 0x1d, 0x63, 0x24, 0x4d, 0x53, 0xad, 0x4f, 0x8c, 0xda, 0x48, 0xb3, 0x08, 0x11}
	if id != want {
		t.Errorf("uuid = %x, want %x", id, want)
	}
	if _, err := parseUUID("short"); err == nil {
		t.Error("want error for short uuid")
	}
}
