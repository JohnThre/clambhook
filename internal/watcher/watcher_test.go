package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
)

const validTOML = `active = "p1"
[[profile]]
name = "p1"
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeAtomic(t *testing.T, path, content string) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename: %v", err)
	}
}

type reloadRecorder struct {
	calls atomic.Int32
	ch    chan *config.Config
}

func newRecorder() *reloadRecorder {
	return &reloadRecorder{ch: make(chan *config.Config, 16)}
}

func (r *reloadRecorder) reload(cfg *config.Config) error {
	r.calls.Add(1)
	select {
	case r.ch <- cfg:
	default:
	}
	return nil
}

func waitForReload(t *testing.T, ch <-chan *config.Config, timeout time.Duration) *config.Config {
	t.Helper()
	select {
	case cfg := <-ch:
		return cfg
	case <-time.After(timeout):
		t.Fatalf("no reload within %s", timeout)
		return nil
	}
}

// startWatcher builds a watcher pointing at path, uses a short debounce for
// test speed, and registers a t.Cleanup that stops it.
func startWatcher(t *testing.T, path string, debounce time.Duration) *reloadRecorder {
	t.Helper()
	rec := newRecorder()
	w, err := New(path, rec.reload, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w.debounce = debounce
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop() })
	return rec
}

func TestWatcherReloadsOnWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	writeFile(t, path, validTOML)

	rec := startWatcher(t, path, 50*time.Millisecond)

	writeFile(t, path, `active = "p2"`+"\n[[profile]]\nname = \"p2\"\n")

	cfg := waitForReload(t, rec.ch, 5*time.Second)
	if cfg.Active != "p2" {
		t.Errorf("got active %q, want p2", cfg.Active)
	}
}

func TestWatcherReloadsOnAtomicSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	writeFile(t, path, validTOML)

	rec := startWatcher(t, path, 50*time.Millisecond)

	writeAtomic(t, path, `active = "p3"`+"\n[[profile]]\nname = \"p3\"\n")

	cfg := waitForReload(t, rec.ch, 5*time.Second)
	if cfg.Active != "p3" {
		t.Errorf("got active %q, want p3", cfg.Active)
	}
}

func TestWatcherSkipsInvalidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	writeFile(t, path, validTOML)

	rec := startWatcher(t, path, 50*time.Millisecond)

	writeFile(t, path, "this is [[ not valid toml")

	// Give the debounce plenty of time to fire (and the fsnotify event
	// plenty of time to propagate on slower backends like kqueue).
	time.Sleep(500 * time.Millisecond)

	if got := rec.calls.Load(); got != 0 {
		t.Errorf("reload was called %d times; want 0", got)
	}
}

func TestWatcherCoalescesRapidWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	writeFile(t, path, validTOML)

	rec := startWatcher(t, path, 100*time.Millisecond)

	for range 5 {
		writeFile(t, path, validTOML)
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for the (single) debounced fire.
	waitForReload(t, rec.ch, 5*time.Second)

	// Confirm no extra fires sneak in after.
	time.Sleep(250 * time.Millisecond)
	if got := rec.calls.Load(); got != 1 {
		t.Errorf("reload was called %d times; want 1", got)
	}
}

func TestWatcherStopReturnsPromptly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	writeFile(t, path, validTOML)

	rec := newRecorder()
	w, err := New(path, rec.reload, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- w.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Stop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Stop did not return within 2s")
	}

	// Second Stop should be a no-op.
	if err := w.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

// TestWatcherStopWaitsForInflightReload proves Stop blocks until an in-flight
// debounced reload callback has finished. Without this guarantee Stop could
// return while a reload was mid-flight, and a caller's teardown (Engine.Stop)
// could then race the callback's Engine.Reload and resurrect listeners.
func TestWatcherStopWaitsForInflightReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	writeFile(t, path, validTOML)

	entered := make(chan struct{})
	release := make(chan struct{})
	var enterOnce sync.Once
	var reloadDone atomic.Bool

	reload := func(cfg *config.Config) error {
		enterOnce.Do(func() { close(entered) })
		<-release
		reloadDone.Store(true)
		return nil
	}

	w, err := New(path, reload, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w.debounce = 20 * time.Millisecond
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Trigger a debounced reload and wait for the callback to enter.
	writeFile(t, path, `active = "p2"`+"\n[[profile]]\nname = \"p2\"\n")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("reload callback never started")
	}

	// Stop must block while the callback is in flight.
	stopDone := make(chan error, 1)
	go func() { stopDone <- w.Stop() }()
	select {
	case <-stopDone:
		t.Fatal("Stop returned while reload callback was still in flight")
	case <-time.After(300 * time.Millisecond):
	}
	if reloadDone.Load() {
		t.Fatal("reload callback finished though it should be blocked")
	}

	// Release the callback; Stop must now complete promptly.
	close(release)
	select {
	case err := <-stopDone:
		if err != nil {
			t.Errorf("Stop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return after reload callback completed")
	}
	if !reloadDone.Load() {
		t.Fatal("reload callback did not finish before Stop returned")
	}

	// Stop stays idempotent after waiting for the in-flight callback.
	if err := w.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

func TestNewValidation(t *testing.T) {
	rec := newRecorder()
	if _, err := New("", rec.reload, nil); err == nil {
		t.Error("expected error for empty path")
	}
	// Passing a nil func should fail.
	if _, err := New(t.TempDir()+"/cfg.toml", nil, nil); err == nil {
		t.Error("expected error for nil reload")
	}
}
