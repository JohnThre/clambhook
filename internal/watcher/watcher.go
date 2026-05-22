// Package watcher reloads the daemon's config file whenever it changes on
// disk. It watches the config file's parent directory (not the file itself)
// so editor save patterns — atomic rename, truncate+write, swap-file dance —
// all produce a reload. A short debounce coalesces bursts of events into a
// single reload. The new config is parsed and validated before it is handed
// to the reload callback, so a typo in TOML never takes the daemon down.
package watcher

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/events"
)

// defaultDebounce is long enough to coalesce an editor's CREATE+WRITE+RENAME
// burst (typically <50ms) but short enough to feel instant to a human.
const defaultDebounce = 250 * time.Millisecond

// ReloadFunc applies a parsed config to the running daemon. The watcher
// invokes it after each successful debounced reload.
type ReloadFunc func(*config.Config) error

// Watcher is a single-file config reloader. Not safe for concurrent Start
// calls; Stop is idempotent and safe from any goroutine.
type Watcher struct {
	path     string
	base     string
	reload   ReloadFunc
	bus      *events.Bus
	debounce time.Duration

	fsw *fsnotify.Watcher

	mu      sync.Mutex
	timer   *time.Timer
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
	stopped bool
}

// New constructs a watcher for path. Resolves the path to absolute form and
// registers the parent directory with fsnotify (required so editor saves via
// atomic rename still trigger events — the file's inode changes, the dir's
// doesn't). bus may be nil, in which case reload outcomes are only logged.
func New(path string, reload ReloadFunc, bus *events.Bus) (*Watcher, error) {
	if path == "" {
		return nil, fmt.Errorf("watcher: empty path")
	}
	if reload == nil {
		return nil, fmt.Errorf("watcher: nil reload func")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("watcher: resolve path: %w", err)
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("watcher: fsnotify init: %w", err)
	}
	dir := filepath.Dir(abs)
	if err := fsw.Add(dir); err != nil {
		_ = fsw.Close()
		return nil, fmt.Errorf("watcher: add %s: %w", dir, err)
	}
	return &Watcher{
		path:     abs,
		base:     filepath.Base(abs),
		reload:   reload,
		bus:      bus,
		debounce: defaultDebounce,
		fsw:      fsw,
		done:     make(chan struct{}),
	}, nil
}

// Start spawns the event loop and returns immediately. Cancelling ctx or
// calling Stop tears the watcher down.
func (w *Watcher) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return fmt.Errorf("watcher: already started")
	}
	w.started = true
	loopCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.mu.Unlock()
	go w.loop(loopCtx)
	return nil
}

// Stop tears the watcher down. Idempotent. Waits for the event loop to exit
// but does not wait for an in-flight fire() — a reload that started just
// before Stop will complete on its own; the caller's ReloadFunc is expected
// to be safe to invoke during shutdown (Engine.Reload takes e.mu).
func (w *Watcher) Stop() error {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return nil
	}
	w.stopped = true
	started := w.started
	if w.cancel != nil {
		w.cancel()
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Unlock()
	err := w.fsw.Close()
	if started {
		<-w.done
	}
	return err
}

func (w *Watcher) loop(ctx context.Context) {
	defer close(w.done)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if filepath.Base(ev.Name) != w.base {
				continue
			}
			// Create | Write | Rename cover every save pattern we care
			// about. Remove on its own means the file is gone; the
			// Create/Rename that follows (if any) will trigger the
			// reload. Chmod alone never indicates a content change.
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) == 0 {
				continue
			}
			w.scheduleReload()
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			log.Printf("watcher: fsnotify error: %v", err)
		}
	}
}

func (w *Watcher) scheduleReload() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	if w.timer == nil {
		w.timer = time.AfterFunc(w.debounce, w.fire)
		return
	}
	w.timer.Reset(w.debounce)
}

func (w *Watcher) fire() {
	cfg, err := config.Load(w.path)
	if err != nil {
		log.Printf("watcher: reload %s: parse failed: %v", w.path, err)
		w.emitFailed(err)
		return
	}
	if err := w.reload(cfg); err != nil {
		log.Printf("watcher: reload %s: apply failed: %v", w.path, err)
		w.emitFailed(err)
		return
	}
	log.Printf("watcher: reloaded %s", w.path)
	if w.bus != nil {
		w.bus.PublishListener(events.TypeConfigReloaded, events.ConfigReloadedData{Path: w.path})
	}
}

func (w *Watcher) emitFailed(err error) {
	if w.bus == nil {
		return
	}
	w.bus.PublishListener(events.TypeConfigReloadFailed, events.ConfigReloadFailedData{
		Path:  w.path,
		Error: err.Error(),
	})
}
