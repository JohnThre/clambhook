package engine

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/clambhook/clambhook/internal/config"
)

// Status represents the engine's current state.
type Status struct {
	Running bool   `json:"running"`
	Profile string `json:"profile"`
}

// Engine manages the connection lifecycle.
type Engine struct {
	cfg     *config.Config
	mu      sync.RWMutex
	running bool
	cancel  context.CancelFunc
}

// New creates a new engine with the given configuration.
func New(cfg *config.Config) *Engine {
	return &Engine{cfg: cfg}
}

// Start begins accepting connections with the active profile.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.running {
		return fmt.Errorf("engine already running")
	}

	profile, err := e.cfg.ActiveProfile()
	if err != nil {
		return fmt.Errorf("start engine: %w", err)
	}

	ctx, e.cancel = context.WithCancel(ctx)
	e.running = true
	log.Printf("engine started with profile %q", profile.Name)

	return nil
}

// Stop shuts down the engine.
func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running {
		return nil
	}

	if e.cancel != nil {
		e.cancel()
	}
	e.running = false
	log.Printf("engine stopped")
	return nil
}

// Reload applies a new configuration.
func (e *Engine) Reload(cfg *config.Config) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg = cfg
	log.Printf("engine configuration reloaded")
	return nil
}

// Status returns the engine's current status.
func (e *Engine) Status() Status {
	e.mu.RLock()
	defer e.mu.RUnlock()

	s := Status{Running: e.running}
	if profile, err := e.cfg.ActiveProfile(); err == nil {
		s.Profile = profile.Name
	}
	return s
}

// Config returns the current configuration.
func (e *Engine) Config() *config.Config {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg
}
