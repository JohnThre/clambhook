package engine

import (
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/listener"
	_ "github.com/JohnThre/clambhook/internal/protocol/trojan"
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

	listeners, chains, policies, err := buildListeners(&profile, nil)
	if !listener.TUNSupported() {
		if err == nil {
			t.Fatal("buildListeners returned nil error for unsupported TUN platform")
		}
		if !strings.Contains(err.Error(), "only supported on Linux and macOS") {
			t.Fatalf("buildListeners error = %q", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("buildListeners: %v", err)
	}
	t.Cleanup(func() { _ = policies.Close() })
	t.Cleanup(func() { _ = closeChains(chains) })
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

func TestBuildPacketStackRejectsDirectDNSHostnameWithoutBootstrap(t *testing.T) {
	profile := config.Profile{
		Name: "default",
		Listen: config.ListenConfig{
			TUN: &config.TUNConfig{
				Enabled: true,
				Chain:   "main",
			},
		},
		DNS: config.DNSConfig{
			Enabled: true,
			Upstreams: []config.DNSUpstreamConfig{{
				Protocol: "dot",
				Address:  "dns.example:853",
			}},
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
		Rules: []config.RuleConfig{{
			Name:     "direct-dot",
			Action:   "direct",
			Domains:  []string{"dns.example"},
			Ports:    []int{853},
			Networks: []string{"tcp"},
		}},
	}

	stack, chains, err := BuildPacketStack(&profile, nil, discardPacketWriter{})
	if stack != nil {
		_ = stack.Stop()
	}
	if closeErr := closeChains(chains); closeErr != nil {
		t.Fatalf("close chains: %v", closeErr)
	}
	if err == nil || !strings.Contains(err.Error(), "needs bootstrap_ips") {
		t.Fatalf("BuildPacketStack error = %v, want bootstrap guard", err)
	}
}

type discardPacketWriter struct{}

func (discardPacketWriter) WritePacket([]byte) error { return nil }
