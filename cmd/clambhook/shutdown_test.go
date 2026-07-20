package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/internal/watcher"
)

// shutdownTestDialer is a no-op protocol dialer. The shutdown-ordering test
// never actually dials through it — it only needs a registered protocol so a
// profile's chain builds and the engine's listeners come up.
type shutdownTestDialer struct{}

func (shutdownTestDialer) Dial(context.Context, string, string) (protocol.Conn, error) {
	return nil, fmt.Errorf("shutdown_ordering: dial not supported in test")
}

func (shutdownTestDialer) DialThrough(_ context.Context, underlying io.ReadWriteCloser, _ string) (protocol.Conn, error) {
	_ = underlying.Close()
	return nil, fmt.Errorf("shutdown_ordering: dial-through not supported in test")
}

func (shutdownTestDialer) Protocol() string { return "shutdown_ordering" }

func init() {
	protocol.Register("shutdown_ordering", func(protocol.Server) (protocol.Dialer, error) {
		return shutdownTestDialer{}, nil
	})
}

func shutdownTestFreePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func shutdownTestConfigTOML(socksAddr string) string {
	return fmt.Sprintf(`active = "local"

[[profile]]
name = "local"

  [profile.listen]
  socks5 = %q

  [[profile.chain]]
  name = "default"

    [[profile.chain.server]]
    name = "s"
    address = "127.0.0.1:1"
    protocol = "shutdown_ordering"
`, socksAddr)
}

// TestShutdownOrderingWaitsForInflightReload exercises the real daemon
// shutdown ordering: the config watcher's reload callback calls Engine.Reload,
// and cmd/clambhook stops the watcher before it stops the engine. If
// Watcher.Stop returned while a reload callback was still in flight, that
// callback's Engine.Reload could run after Engine.Stop and resurrect
// listeners. This test blocks a reload mid-flight, proves cfgWatcher.Stop()
// does not return until the callback finishes, and confirms the engine is
// cleanly stopped once the shutdown sequence completes.
func TestShutdownOrderingWaitsForInflightReload(t *testing.T) {
	socksAddr := shutdownTestFreePort(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	if err := os.WriteFile(path, []byte(shutdownTestConfigTOML(socksAddr)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	eng := engine.New(cfg, nil)
	if err := eng.Start(context.Background()); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Stop() })

	if !eng.Status().Running {
		t.Fatal("engine should be running after Start")
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var enterOnce sync.Once
	var reloadReturned atomic.Bool

	// Mirror cmd/clambhook's reload wiring: the callback drives Engine.Reload.
	// The block simulates a slow reload that is still in flight when shutdown
	// begins.
	cfgWatcher, err := watcher.New(path, func(next *config.Config) error {
		enterOnce.Do(func() { close(entered) })
		<-release
		err := eng.Reload(next)
		reloadReturned.Store(true)
		return err
	}, nil)
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	if err := cfgWatcher.Start(context.Background()); err != nil {
		t.Fatalf("start watcher: %v", err)
	}

	// Trigger a reload and wait for the callback to enter.
	if err := os.WriteFile(path, []byte(shutdownTestConfigTOML(socksAddr)), 0o644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("reload callback never started")
	}

	// Begin shutdown: cfgWatcher.Stop() must block until the in-flight reload
	// callback finishes.
	stopDone := make(chan error, 1)
	go func() { stopDone <- cfgWatcher.Stop() }()
	select {
	case <-stopDone:
		t.Fatal("cfgWatcher.Stop() returned while reload callback still in flight")
	case <-time.After(300 * time.Millisecond):
	}
	if reloadReturned.Load() {
		t.Fatal("reload callback finished though it should be blocked")
	}

	// Release the callback; Stop must now complete.
	close(release)
	select {
	case err := <-stopDone:
		if err != nil {
			t.Errorf("cfgWatcher.Stop(): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cfgWatcher.Stop() did not return after reload completed")
	}
	if !reloadReturned.Load() {
		t.Fatal("reload callback did not finish before cfgWatcher.Stop() returned")
	}

	// With the watcher fully stopped and no reload in flight, stopping the
	// engine cannot be raced by a resurrecting Reload.
	if err := eng.Stop(); err != nil {
		t.Errorf("engine Stop: %v", err)
	}
	if eng.Status().Running {
		t.Fatal("engine still running after shutdown sequence")
	}

	// Watcher stop stays idempotent.
	if err := cfgWatcher.Stop(); err != nil {
		t.Errorf("second cfgWatcher.Stop(): %v", err)
	}
}
