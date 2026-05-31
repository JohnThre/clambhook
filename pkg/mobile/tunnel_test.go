package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTunnelNetworkSettingsJSONAppliesMobileDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clambhook.toml")
	if err := os.WriteFile(path, []byte(`
active = "default"

[[profile]]
name = "default"

  [[profile.chain]]
  name = "proxy"

    [[profile.chain.server]]
    name = "example"
    address = "example.invalid:443"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "chacha20-ietf-poly1305"
      password = "secret"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	raw, err := TunnelNetworkSettingsJSON(path)
	if err != nil {
		t.Fatal(err)
	}

	var payload tunnelNetworkSettings
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MTU != defaultTunnelMTU {
		t.Fatalf("MTU = %d, want %d", payload.MTU, defaultTunnelMTU)
	}
	if payload.RemoteAddress != "example.invalid" {
		t.Fatalf("remote address = %q, want example.invalid", payload.RemoteAddress)
	}
	if len(payload.IPv4) != 1 || payload.IPv4[0].Address != "198.18.0.1" || payload.IPv4[0].PrefixLen != 30 {
		t.Fatalf("unexpected IPv4 settings: %#v", payload.IPv4)
	}
	if len(payload.IPv6) != 1 || payload.IPv6[0].Address != "fd7a:636c:616d::1" || payload.IPv6[0].PrefixLen != 64 {
		t.Fatalf("unexpected IPv6 settings: %#v", payload.IPv6)
	}
	if len(payload.IncludedRoutes) != 2 || payload.IncludedRoutes[0] != "0.0.0.0/0" || payload.IncludedRoutes[1] != "::/0" {
		t.Fatalf("unexpected included routes: %#v", payload.IncludedRoutes)
	}
}
