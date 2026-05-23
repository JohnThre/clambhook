package engine

import (
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
)

func TestBuildListenersIncludesEnabledTUN(t *testing.T) {
	profile := config.Profile{
		Name: "default",
		Listen: config.ListenConfig{
			TUN: &config.TUNConfig{
				Enabled: true,
				Name:    "clambhook-test0",
				Chain:   "main",
			},
		},
		Chains: []config.ChainConfig{{
			Name: "main",
			Servers: []config.ServerConfig{{
				Name:     "exit",
				Address:  "203.0.113.10:443",
				Protocol: "trojan",
				Settings: map[string]any{"password": "secret"},
			}},
		}},
	}

	listeners, chains, err := buildListeners(&profile, nil)
	if err != nil {
		t.Fatalf("buildListeners: %v", err)
	}
	if len(chains) != 1 {
		t.Fatalf("len(chains) = %d, want 1", len(chains))
	}
	if len(listeners) != 1 {
		t.Fatalf("len(listeners) = %d, want 1", len(listeners))
	}
	if listeners[0].Protocol() != "tun" {
		t.Fatalf("Protocol = %q, want tun", listeners[0].Protocol())
	}
	if listeners[0].Addr() != "clambhook-test0" {
		t.Fatalf("Addr = %q, want clambhook-test0", listeners[0].Addr())
	}
}
