//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestRequiredProtocolBackendsPresent prevents scheduled/manual CI from going
// green after the real server processes disappear. Individual compatibility
// tests may remain convenient and skip-capable for local, non-required runs.
func TestRequiredProtocolBackendsPresent(t *testing.T) {
	requireE2E(t)
	if os.Getenv("CLAMBHOOK_E2E_REQUIRE_BACKENDS") != "1" {
		t.Skip("backend enforcement is enabled by make e2e-required")
	}

	if !commandExists("tor") {
		t.Error("required protocol backend tor is not on PATH")
	}

	switch backend := os.Getenv("CLAMBHOOK_E2E_BACKEND"); backend {
	case "", "auto":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !commandExists("sing-box") && !dockerUsable(ctx) {
			t.Error("required sing-box backend is unavailable: install sing-box or provide usable Docker")
		}
	case "local":
		if !commandExists("sing-box") {
			t.Error("required local sing-box backend is not on PATH")
		}
	case "docker":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !dockerUsable(ctx) {
			t.Error("required Docker sing-box backend is unavailable")
		}
	default:
		t.Errorf("unsupported CLAMBHOOK_E2E_BACKEND=%q", backend)
	}

	clambback := os.Getenv("CLAMBBACK_BIN")
	if clambback == "" {
		t.Error("required first-party ClambBack backend is missing: CLAMBBACK_BIN is empty")
		return
	}
	info, err := os.Stat(clambback)
	if err != nil {
		t.Errorf("required first-party ClambBack backend %q is unusable: %v", clambback, err)
		return
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		t.Errorf("required first-party ClambBack backend %q is not executable", clambback)
	}
}
