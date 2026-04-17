package engine

import (
	"context"
	"net"
	"testing"

	"github.com/clambhook/clambhook/internal/config"
)

// fixedPortProfile returns a minimal profile with one chain and the given
// SOCKS5 listen address. The chain's protocol is never dialed (no client
// connects in these tests) so its validity doesn't matter.
func fixedPortProfile(name, socksAddr string) config.Profile {
	return config.Profile{
		Name:   name,
		Listen: config.ListenConfig{SOCKS5: socksAddr},
		Chains: []config.ChainConfig{{
			Name: "default",
			Servers: []config.ServerConfig{{
				Name:     "dummy",
				Address:  "127.0.0.1:1",
				Protocol: "trojan",
				Settings: map[string]any{"password": "x"},
			}},
		}},
	}
}

// freePort returns a port number that was briefly bound then released —
// good enough for a local test where the port is re-bound within
// milliseconds.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestEngineSetActiveProfileRebuildsListeners(t *testing.T) {
	addrA := freePort(t)
	addrB := freePort(t)

	cfg := &config.Config{
		Active: "A",
		Profiles: []config.Profile{
			fixedPortProfile("A", addrA),
			fixedPortProfile("B", addrB),
		},
	}

	e := New(cfg)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer e.Stop()

	// Port A should be bound.
	if _, err := net.Listen("tcp", addrA); err == nil {
		t.Errorf("expected %s already bound, but we took it", addrA)
	}

	if err := e.SetActiveProfile("B"); err != nil {
		t.Fatalf("switch: %v", err)
	}

	// Port A should be free now.
	la, err := net.Listen("tcp", addrA)
	if err != nil {
		t.Errorf("expected %s released after profile switch: %v", addrA, err)
	} else {
		la.Close()
	}

	// Port B should be bound.
	if _, err := net.Listen("tcp", addrB); err == nil {
		t.Errorf("expected %s bound after profile switch", addrB)
	}

	// Status reflects the new profile.
	status := e.Status()
	if status.Profile != "B" {
		t.Errorf("status.Profile = %q, want %q", status.Profile, "B")
	}
	if len(status.Listeners) != 1 || status.Listeners[0].Addr != addrB {
		t.Errorf("status.Listeners = %+v, want one bound at %s", status.Listeners, addrB)
	}
}

func TestEngineSetActiveProfileNotFound(t *testing.T) {
	cfg := &config.Config{
		Active:   "A",
		Profiles: []config.Profile{fixedPortProfile("A", freePort(t))},
	}
	e := New(cfg)
	if err := e.SetActiveProfile("bogus"); err == nil {
		t.Error("expected error for unknown profile")
	}
}

func TestEngineReloadIdle(t *testing.T) {
	cfg := &config.Config{
		Active:   "A",
		Profiles: []config.Profile{fixedPortProfile("A", freePort(t))},
	}
	e := New(cfg)
	// Reload before Start — should just swap config without error.
	cfg2 := &config.Config{
		Active:   "B",
		Profiles: []config.Profile{fixedPortProfile("B", freePort(t))},
	}
	if err := e.Reload(cfg2); err != nil {
		t.Errorf("reload idle: %v", err)
	}
	if e.Config().Active != "B" {
		t.Error("reload did not replace config")
	}
}
