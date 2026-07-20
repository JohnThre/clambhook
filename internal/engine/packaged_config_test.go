package engine

import (
	"context"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
)

// The .deb/.rpm packages ship packaging/config/config.toml as the default
// /etc/clambhook/config.toml so the systemd unit
// (packaging/systemd/clambhook-daemon.service, ExecStart ... -config
// /etc/clambhook/config.toml) starts cleanly on a fresh install instead of
// crash-looping. This test guards that contract: the packaged default must
// load, pass runtime validation, and let the engine start and stop without a
// fatal error.
func TestPackagedDefaultConfigBoots(t *testing.T) {
	const path = "../../packaging/config/config.toml"

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load packaged default config %q: %v", path, err)
	}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("packaged default config failed runtime validation: %v", err)
	}

	eng := New(cfg, nil)
	if err := eng.Start(context.Background()); err != nil {
		t.Fatalf("engine did not start with packaged default config: %v", err)
	}
	if err := eng.Stop(); err != nil {
		t.Fatalf("engine stop: %v", err)
	}
}
