package tunnelcore

import (
	"encoding/json"
	"testing"
)

func TestManagerLoadsProfilesFromTOML(t *testing.T) {
	mgr, err := NewManager(`
active = "default"

[[profile]]
name = "default"

  [[profile.chain]]
  name = "direct"

    [[profile.chain.server]]
    name = "proxy"
    address = "127.0.0.1:8388"
    protocol = "shadowsocks"
    [profile.chain.server.settings]
    method = "aes-128-gcm"
    password = "secret"
`)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	var profiles []string
	if err := json.Unmarshal([]byte(mgr.ProfileNamesJSON()), &profiles); err != nil {
		t.Fatalf("ProfileNamesJSON: %v", err)
	}
	if len(profiles) != 1 || profiles[0] != "default" {
		t.Fatalf("profiles = %#v, want [default]", profiles)
	}

	status := mgr.StatusJSON()
	if !json.Valid([]byte(status)) {
		t.Fatalf("StatusJSON is not JSON: %q", status)
	}
}

func TestManagerRejectsEmptyConfig(t *testing.T) {
	if _, err := NewManager("  \n"); err == nil {
		t.Fatal("NewManager empty config: expected error")
	}
}

func TestSupportedProtocolsIncludesDaemonProtocols(t *testing.T) {
	var protocols []string
	if err := json.Unmarshal([]byte(SupportedProtocolsJSON()), &protocols); err != nil {
		t.Fatalf("SupportedProtocolsJSON: %v", err)
	}

	for _, want := range []string{"openvpn", "reality", "shadowsocks", "tor", "trojan", "vless", "vmess", "wireguard"} {
		if !contains(protocols, want) {
			t.Fatalf("SupportedProtocolsJSON missing %q in %#v", want, protocols)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
