package tor

import (
	"strings"
	"testing"

	"github.com/clambhook/clambhook/internal/protocol"
)

func TestParseConfigRequiresAddress(t *testing.T) {
	_, err := parseConfig(protocol.Server{Settings: map[string]any{}})
	if err == nil {
		t.Fatal("expected error for empty address")
	}
	if !strings.Contains(err.Error(), "address is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseConfigInvalidAddress(t *testing.T) {
	_, err := parseConfig(protocol.Server{
		Address:  "not-a-host-port",
		Settings: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for malformed address")
	}
}

func TestParseConfigHappyPath(t *testing.T) {
	cfg, err := parseConfig(protocol.Server{
		Address:  "127.0.0.1:9050",
		Settings: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.socksAddr != "127.0.0.1:9050" {
		t.Fatalf("socksAddr = %q", cfg.socksAddr)
	}
	if cfg.isolationUser != "" || cfg.isolationPass != "" {
		t.Fatalf("expected empty isolation creds, got %q/%q", cfg.isolationUser, cfg.isolationPass)
	}
}

func TestParseConfigIsolationPair(t *testing.T) {
	cfg, err := parseConfig(protocol.Server{
		Address: "127.0.0.1:9050",
		Settings: map[string]any{
			"isolation_user": "clambhook",
			"isolation_pass": "profile-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.isolationUser != "clambhook" || cfg.isolationPass != "profile-1" {
		t.Fatalf("unexpected creds: %q/%q", cfg.isolationUser, cfg.isolationPass)
	}
}

func TestParseConfigPartialIsolationRejected(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings map[string]any
	}{
		{"user only", map[string]any{"isolation_user": "x"}},
		{"pass only", map[string]any{"isolation_pass": "y"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseConfig(protocol.Server{
				Address:  "127.0.0.1:9050",
				Settings: tc.settings,
			})
			if err == nil {
				t.Fatal("expected error for half-set isolation creds")
			}
		})
	}
}
