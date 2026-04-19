package wireguard

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/clambhook/clambhook/internal/protocol"
)

// validKeyB64 is 32 zero bytes encoded as base64. Suitable for parser
// tests where the key value isn't being exercised cryptographically.
var validKeyB64 = base64.StdEncoding.EncodeToString(make([]byte, 32))

// validPeer is a minimum-fields peer block, reused across happy-path
// tests so the per-test setup stays focused on the field under test.
func validPeer() map[string]any {
	return map[string]any{
		"public_key":  validKeyB64,
		"endpoint":    "1.2.3.4:51820",
		"allowed_ips": []any{"0.0.0.0/0"},
	}
}

func validServer() protocol.Server {
	return protocol.Server{
		Name:    "wg-test",
		Address: "1.2.3.4:51820",
		Settings: map[string]any{
			"private_key": validKeyB64,
			"addresses":   []any{"10.0.0.2/32"},
			"peers":       []map[string]any{validPeer()},
		},
	}
}

func TestParseConfigHappyPath(t *testing.T) {
	cfg, err := parseConfig(validServer())
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if len(cfg.privateKeyHex) != 64 {
		t.Errorf("private key hex len = %d, want 64", len(cfg.privateKeyHex))
	}
	if len(cfg.addresses) != 1 {
		t.Errorf("addresses = %v, want 1 entry", cfg.addresses)
	}
	if len(cfg.peers) != 1 {
		t.Errorf("peers = %v, want 1 entry", cfg.peers)
	}
	if cfg.mtu != 1420 {
		t.Errorf("mtu = %d, want default 1420", cfg.mtu)
	}
}

func TestParseConfigMissingPrivateKey(t *testing.T) {
	s := validServer()
	delete(s.Settings, "private_key")
	_, err := parseConfig(s)
	if err == nil || !strings.Contains(err.Error(), "private_key") {
		t.Fatalf("expected private_key error, got %v", err)
	}
}

func TestParseConfigMissingAddresses(t *testing.T) {
	s := validServer()
	delete(s.Settings, "addresses")
	_, err := parseConfig(s)
	if err == nil || !strings.Contains(err.Error(), "addresses") {
		t.Fatalf("expected addresses error, got %v", err)
	}
}

func TestParseConfigInvalidAddressCIDR(t *testing.T) {
	s := validServer()
	s.Settings["addresses"] = []any{"not-a-cidr"}
	_, err := parseConfig(s)
	if err == nil || !strings.Contains(err.Error(), "invalid address") {
		t.Fatalf("expected invalid address error, got %v", err)
	}
}

func TestParseConfigMissingPeers(t *testing.T) {
	s := validServer()
	delete(s.Settings, "peers")
	_, err := parseConfig(s)
	if err == nil || !strings.Contains(err.Error(), "peer") {
		t.Fatalf("expected peers error, got %v", err)
	}
}

func TestParseConfigPeerMissingPublicKey(t *testing.T) {
	s := validServer()
	p := validPeer()
	delete(p, "public_key")
	s.Settings["peers"] = []map[string]any{p}
	_, err := parseConfig(s)
	if err == nil || !strings.Contains(err.Error(), "public_key") {
		t.Fatalf("expected public_key error, got %v", err)
	}
}

func TestParseConfigPeerMissingEndpoint(t *testing.T) {
	s := validServer()
	p := validPeer()
	delete(p, "endpoint")
	s.Settings["peers"] = []map[string]any{p}
	_, err := parseConfig(s)
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("expected endpoint error, got %v", err)
	}
}

func TestParseConfigPeerInvalidAllowedIPs(t *testing.T) {
	s := validServer()
	p := validPeer()
	p["allowed_ips"] = []any{"not-a-cidr"}
	s.Settings["peers"] = []map[string]any{p}
	_, err := parseConfig(s)
	if err == nil || !strings.Contains(err.Error(), "allowed_ips") {
		t.Fatalf("expected allowed_ips error, got %v", err)
	}
}

func TestParseConfigEndpointMismatch(t *testing.T) {
	s := validServer()
	s.Address = "5.6.7.8:51820"
	_, err := parseConfig(s)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected mismatch error, got %v", err)
	}
}

func TestParseConfigEndpointAddressEmpty(t *testing.T) {
	// Empty top-level address is permitted — the peer endpoint is
	// authoritative for transport. This keeps the protocol usable in
	// configs that prefer to keep the endpoint only in the peer block.
	s := validServer()
	s.Address = ""
	if _, err := parseConfig(s); err != nil {
		t.Fatalf("empty Address should be allowed: %v", err)
	}
}

func TestParseConfigOptionalDNS(t *testing.T) {
	s := validServer()
	s.Settings["dns"] = []any{"1.1.1.1", "8.8.8.8"}
	cfg, err := parseConfig(s)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if len(cfg.dns) != 2 {
		t.Errorf("dns = %v, want 2 entries", cfg.dns)
	}
}

func TestParseConfigInvalidDNS(t *testing.T) {
	s := validServer()
	s.Settings["dns"] = []any{"not-an-ip"}
	if _, err := parseConfig(s); err == nil {
		t.Fatal("expected dns parse error")
	}
}

func TestParseConfigMTUOverride(t *testing.T) {
	s := validServer()
	s.Settings["mtu"] = int64(1280)
	cfg, err := parseConfig(s)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.mtu != 1280 {
		t.Errorf("mtu = %d, want 1280", cfg.mtu)
	}
}

func TestParseConfigInvalidLogLevel(t *testing.T) {
	s := validServer()
	s.Settings["log_level"] = "noisy"
	if _, err := parseConfig(s); err == nil {
		t.Fatal("expected log_level error")
	}
}

func TestParseConfigPSK(t *testing.T) {
	s := validServer()
	p := validPeer()
	p["preshared_key"] = validKeyB64
	s.Settings["peers"] = []map[string]any{p}
	cfg, err := parseConfig(s)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.peers[0].presharedKeyHex == "" {
		t.Error("expected preshared_key to be set")
	}
}

func TestParseConfigKeepaliveOutOfRange(t *testing.T) {
	s := validServer()
	p := validPeer()
	p["persistent_keepalive"] = int64(-5)
	s.Settings["peers"] = []map[string]any{p}
	if _, err := parseConfig(s); err == nil {
		t.Fatal("expected keepalive range error")
	}
}

// TestBuildUAPIConfigOrder verifies the IpcSet wire format keeps
// per-peer blocks contiguous: each public_key= must be followed by its
// own endpoint and allowed_ip lines, never interleaved with another
// peer's. Wireguard-go silently merges interleaved peers (they end up
// with a combined allowed_ips list), which is the kind of bug a config
// reader would never spot.
func TestBuildUAPIConfigOrder(t *testing.T) {
	cfg := &config{
		privateKeyHex: "aa",
		peers: []peerConfig{
			{publicKeyHex: "bb", endpoint: "1.1.1.1:1", allowedIPs: []string{"10.0.0.0/8"}, keepalive: 25},
			{publicKeyHex: "cc", endpoint: "2.2.2.2:2", allowedIPs: []string{"192.168.0.0/16"}, presharedKeyHex: "dd"},
		},
	}
	out := buildUAPIConfig(cfg)
	wantOrder := []string{
		"private_key=aa",
		"public_key=bb",
		"endpoint=1.1.1.1:1",
		"persistent_keepalive_interval=25",
		"replace_allowed_ips=true",
		"allowed_ip=10.0.0.0/8",
		"public_key=cc",
		"preshared_key=dd",
		"endpoint=2.2.2.2:2",
		"replace_allowed_ips=true",
		"allowed_ip=192.168.0.0/16",
	}
	cursor := 0
	for _, want := range wantOrder {
		// Search from cursor forward so duplicate strings (e.g.
		// `replace_allowed_ips=true` appears once per peer) match the
		// next occurrence, not the first.
		idx := strings.Index(out[cursor:], want)
		if idx < 0 {
			t.Errorf("missing line %q after offset %d in:\n%s", want, cursor, out)
			continue
		}
		cursor += idx + len(want)
	}
}
