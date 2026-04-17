package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/clambhook/clambhook/internal/chain"
	"github.com/clambhook/clambhook/internal/config"
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
	Protocol string `json:"protocol"`
	Addr     string `json:"addr"`
}

// Engine manages the connection lifecycle.
type Engine struct {
	cfg       *config.Config
	mu        sync.RWMutex
	running   bool
	cancel    context.CancelFunc
	listeners []listener.Listener
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

	listeners, err := buildListeners(profile)
	if err != nil {
		e.cancel()
		e.cancel = nil
		return fmt.Errorf("start engine: %w", err)
	}

	for i, l := range listeners {
		if err := l.Start(ctx); err != nil {
			// Roll back the listeners we already started.
			for j := 0; j < i; j++ {
				if stopErr := listeners[j].Stop(); stopErr != nil {
					log.Printf("engine: rollback stop %s: %v", listeners[j].Protocol(), stopErr)
				}
			}
			e.cancel()
			e.cancel = nil
			return fmt.Errorf("start %s: %w", l.Protocol(), err)
		}
	}

	e.listeners = listeners
	e.running = true
	log.Printf("engine started with profile %q (%d listeners)", profile.Name, len(listeners))
	return nil
}

// Stop shuts down the engine.
func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running {
		return nil
	}

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
	log.Printf("engine stopped")
	return errors.Join(errs...)
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
	for _, l := range e.listeners {
		s.Listeners = append(s.Listeners, ListenerStatus{
			Protocol: l.Protocol(),
			Addr:     l.Addr(),
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
