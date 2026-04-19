package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/clambhook/clambhook/internal/chain"
	"github.com/clambhook/clambhook/internal/config"
	"github.com/clambhook/clambhook/internal/geo"
	"github.com/clambhook/clambhook/internal/listener"
	"github.com/clambhook/clambhook/internal/protocol"
)

// defaultSOCKS5MaxConns is the default concurrent-handler ceiling when the
// profile doesn't set socks5_max_connections. Generous for personal use;
// bounds the blast radius of a runaway client.
const defaultSOCKS5MaxConns = 512

// Status represents the engine's current state.
type Status struct {
	Running   bool             `json:"running"`
	Profile   string           `json:"profile"`
	Listeners []ListenerStatus `json:"listeners,omitempty"`
}

// ListenerStatus reports a single active listener.
type ListenerStatus struct {
	Protocol    string `json:"protocol"`
	Addr        string `json:"addr"`
	ActiveConns int64  `json:"active_conns"`
}

// Engine manages the connection lifecycle.
type Engine struct {
	cfg       *config.Config
	mu        sync.RWMutex
	running   bool
	cancel    context.CancelFunc
	listeners []listener.Listener
	geoReader *geo.Reader
}

// New creates a new engine with the given configuration. If a geo database
// is configured but fails to open, the error is logged and geo stays
// disabled — a bad geo path must never prevent the daemon from starting.
func New(cfg *config.Config) *Engine {
	e := &Engine{cfg: cfg}
	if r, err := geo.Open(cfg.Geo.Database); err != nil {
		log.Printf("geo: %v; continuing without geo lookups", err)
	} else if r != nil {
		log.Printf("geo: opened %q", cfg.Geo.Database)
		e.geoReader = r
	}
	return e
}

// Start begins accepting connections with the active profile.
//
// The supplied ctx is used only for orchestrating the startup itself (e.g.,
// cancelling a slow listener bind). Listener lifetime is governed by the
// engine's own internal context; callers with a short-lived ctx (like an
// HTTP handler) can safely return without tearing listeners down.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.running {
		return fmt.Errorf("engine already running")
	}
	return e.startLocked()
}

// Stop shuts down the engine.
func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running {
		return nil
	}
	err := e.stopLocked()
	log.Printf("engine stopped")
	return err
}

// Reload applies a new configuration. If the engine is currently running,
// listeners are torn down and rebuilt against the new profile — so a switch
// of active profile or a listener-affecting config change takes effect
// without requiring an explicit disconnect/connect cycle.
func (e *Engine) Reload(cfg *config.Config) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	oldGeoPath := e.cfg.Geo.Database
	e.cfg = cfg
	if cfg.Geo.Database != oldGeoPath {
		e.swapGeoLocked()
	}
	if !e.running {
		log.Printf("engine configuration reloaded (idle)")
		return nil
	}

	// Restart listeners against the new profile. If startup fails we leave
	// the engine stopped — the caller can inspect Status and retry with a
	// corrected config.
	if err := e.stopLocked(); err != nil {
		log.Printf("reload: stop listeners: %v", err)
	}
	if err := e.startLocked(); err != nil {
		return fmt.Errorf("reload: restart: %w", err)
	}
	log.Printf("engine reloaded live — listeners rebuilt")
	return nil
}

// SetActiveProfile switches the active profile and, if running, rebuilds
// listeners for it. Returns an error if the profile isn't defined.
func (e *Engine) SetActiveProfile(name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.cfg.ProfileByName(name); !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	e.cfg.Active = name
	if !e.running {
		return nil
	}
	if err := e.stopLocked(); err != nil {
		log.Printf("profile switch: stop listeners: %v", err)
	}
	if err := e.startLocked(); err != nil {
		return fmt.Errorf("profile switch: restart: %w", err)
	}
	log.Printf("engine switched to profile %q — listeners rebuilt", name)
	return nil
}

// startLocked performs the actual listener setup. Caller holds e.mu.
func (e *Engine) startLocked() error {
	profile, err := e.cfg.ActiveProfile()
	if err != nil {
		return fmt.Errorf("start engine: %w", err)
	}

	// Engine owns its own context — independent of any caller's ctx. This
	// lets short-lived callers (HTTP handlers, CLI one-shots) invoke Start
	// without their ctx cancellation tearing listeners down.
	ctx, cancel := context.WithCancel(context.Background())

	listeners, err := buildListeners(profile)
	if err != nil {
		cancel()
		return fmt.Errorf("start engine: %w", err)
	}

	for i, l := range listeners {
		if startErr := l.Start(ctx); startErr != nil {
			// Roll back the listeners we already started.
			for j := 0; j < i; j++ {
				if stopErr := listeners[j].Stop(); stopErr != nil {
					log.Printf("engine: rollback stop %s: %v", listeners[j].Protocol(), stopErr)
				}
			}
			cancel()
			return fmt.Errorf("start %s: %w", l.Protocol(), startErr)
		}
	}

	e.cancel = cancel
	e.listeners = listeners
	e.running = true
	log.Printf("engine started with profile %q (%d listeners)", profile.Name, len(listeners))
	return nil
}

// stopLocked performs the actual listener teardown. Caller holds e.mu.
func (e *Engine) stopLocked() error {
	var errs []error
	for _, l := range e.listeners {
		if err := l.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", l.Protocol(), err))
		}
	}
	e.listeners = nil

	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	e.running = false
	return errors.Join(errs...)
}

// Status returns the engine's current status.
func (e *Engine) Status() Status {
	e.mu.RLock()
	defer e.mu.RUnlock()

	s := Status{Running: e.running}
	if profile, err := e.cfg.ActiveProfile(); err == nil {
		s.Profile = profile.Name
	}
	for _, l := range e.listeners {
		s.Listeners = append(s.Listeners, ListenerStatus{
			Protocol:    l.Protocol(),
			Addr:        l.Addr(),
			ActiveConns: l.ActiveConns(),
		})
	}
	return s
}

// Config returns the current configuration.
func (e *Engine) Config() *config.Config {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg
}

// GeoReader returns the current geo reader. The returned value may be nil
// if geo is disabled or failed to load — callers treat nil as "disabled"
// (Reader.Lookup is nil-safe).
func (e *Engine) GeoReader() *geo.Reader {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.geoReader
}

// CloseGeo releases the geo database handle. Separate from Stop because
// Reload-while-stopped can still update the geo DB; Stop only tears down
// listeners. Safe to call when geo is disabled.
func (e *Engine) CloseGeo() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.geoReader
	e.geoReader = nil
	return r.Close()
}

// swapGeoLocked opens the DB at e.cfg.Geo.Database and replaces the
// current geoReader. On failure the old reader is preserved so live
// lookups survive a bad config. Caller holds e.mu.
func (e *Engine) swapGeoLocked() {
	newR, err := geo.Open(e.cfg.Geo.Database)
	if err != nil {
		log.Printf("geo: reload failed (%v); keeping previous reader", err)
		return
	}
	old := e.geoReader
	e.geoReader = newR
	if old != nil {
		if err := old.Close(); err != nil {
			log.Printf("geo: closing previous reader: %v", err)
		}
	}
	if newR != nil {
		log.Printf("geo: swapped to %q", e.cfg.Geo.Database)
	} else {
		log.Printf("geo: disabled (database path cleared)")
	}
}

// buildListeners constructs all listeners configured on the active profile.
// It does not start them — Start does that so partial-startup can be rolled
// back cleanly.
func buildListeners(profile *config.Profile) ([]listener.Listener, error) {
	var out []listener.Listener

	if addr := profile.Listen.SOCKS5; addr != "" {
		ch, err := resolveChain(profile, profile.Listen.SOCKS5Chain)
		if err != nil {
			return nil, fmt.Errorf("socks5: %w", err)
		}
		var auth *listener.AuthCreds
		if profile.Listen.SOCKS5Auth != nil {
			auth = &listener.AuthCreds{
				Username: profile.Listen.SOCKS5Auth.Username,
				Password: profile.Listen.SOCKS5Auth.Password,
			}
		}
		maxConns := profile.Listen.SOCKS5MaxConns
		if maxConns == 0 {
			// Default ceiling — generous enough for a personal proxy but bounded
			// so a runaway client can't exhaust FDs. Operators set 0 explicitly
			// via config is treated the same; override with any positive int.
			maxConns = defaultSOCKS5MaxConns
		}
		opts := listener.Options{
			MaxConnections:   maxConns,
			HandshakeTimeout: profile.Listen.SOCKS5HandshakeTimeout.Std(),
		}
		out = append(out, listener.NewSOCKSv5(addr, auth, ch, opts))
	}

	return out, nil
}

// resolveChain picks the chain a listener should route through. An empty
// name selects the first chain in the profile — this matches the plan's
// decision to keep routing implicit until rule-based routing lands.
func resolveChain(profile *config.Profile, name string) (*chain.Chain, error) {
	if len(profile.Chains) == 0 {
		return nil, errors.New("profile has no chains")
	}
	if name == "" {
		return chainFromConfig(profile.Chains[0]), nil
	}
	for i := range profile.Chains {
		if profile.Chains[i].Name == name {
			return chainFromConfig(profile.Chains[i]), nil
		}
	}
	return nil, fmt.Errorf("chain %q not found", name)
}

// chainFromConfig converts a TOML-parsed chain stanza into the protocol-layer
// chain.Chain type. It lives here (rather than in internal/chain) to keep
// chain free of a dependency on internal/config.
func chainFromConfig(cfg config.ChainConfig) *chain.Chain {
	nodes := make([]protocol.Server, len(cfg.Servers))
	for i, s := range cfg.Servers {
		nodes[i] = protocol.Server{
			Name:     s.Name,
			Address:  s.Address,
			Protocol: s.Protocol,
			Settings: s.Settings,
		}
	}
	return &chain.Chain{Name: cfg.Name, Nodes: nodes}
}
