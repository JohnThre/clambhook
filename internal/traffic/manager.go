package traffic

import (
	"context"
	"sync"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/events"
)

// Manager owns the live traffic store and its event-bus subscription. It lets
// config reloads enable, disable, or retune traffic history without requiring
// daemon restart.
type Manager struct {
	mu        sync.RWMutex
	cfg       config.TrafficConfig
	geoLookup GeoLookupFunc

	store *Store
	bus   *events.Bus

	ctx         context.Context
	cancel      context.CancelFunc
	storeCancel context.CancelFunc
}

// NewManager creates a traffic manager with the initial store, if enabled.
func NewManager(cfg config.TrafficConfig, geoLookup GeoLookupFunc) (*Manager, error) {
	store, err := NewStore(cfg, geoLookup)
	if err != nil {
		return nil, err
	}
	return &Manager{
		cfg:       cfg,
		geoLookup: geoLookup,
		store:     store,
	}, nil
}

// Start subscribes the current store to bus and keeps enough state for future
// Reconfigure calls to resubscribe replacement stores.
func (m *Manager) Start(ctx context.Context, bus *events.Bus) {
	if m == nil || bus == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		return
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.bus = bus
	m.startStoreLocked()
}

// Stop cancels all traffic subscriptions. It is idempotent.
func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	if m.storeCancel != nil {
		m.storeCancel()
		m.storeCancel = nil
	}
	m.ctx = nil
	m.bus = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Store returns the current traffic store. nil means traffic is disabled.
func (m *Manager) Store() *Store {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store
}

// Reconfigure applies traffic settings from a config reload.
func (m *Manager) Reconfigure(cfg config.TrafficConfig) error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg

	if !cfg.Enabled {
		if m.storeCancel != nil {
			m.storeCancel()
			m.storeCancel = nil
		}
		if m.store != nil {
			_ = m.store.Reconfigure(cfg)
		}
		m.store = nil
		return nil
	}

	if m.store == nil {
		store, err := NewStore(cfg, m.geoLookup)
		if err != nil {
			return err
		}
		m.store = store
		m.startStoreLocked()
		return nil
	}
	return m.store.Reconfigure(cfg)
}

func (m *Manager) startStoreLocked() {
	if m.store == nil || m.bus == nil || m.ctx == nil || m.storeCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.storeCancel = cancel
	m.store.Start(ctx, m.bus)
}
