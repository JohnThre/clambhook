// Package tunnelcore exposes a small, gomobile-friendly boundary for the
// iOS packet tunnel extension.
package tunnelcore

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/clambhook/clambhook/internal/chain"
	"github.com/clambhook/clambhook/internal/config"
	"github.com/clambhook/clambhook/internal/protocol"
)

type Manager struct {
	mu      sync.Mutex
	toml    string
	cfg     *config.Config
	running bool
	started time.Time
	packet  *packetTunnel
}

type statusPayload struct {
	Running       bool     `json:"running"`
	ActiveProfile string   `json:"active_profile"`
	Profiles      []string `json:"profiles"`
	StartedAtUnix int64    `json:"started_at_unix,omitempty"`
}

func NewManager(configTOML string) (*Manager, error) {
	m := &Manager{}
	if err := m.SetConfig(configTOML); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) SetConfig(configTOML string) error {
	if strings.TrimSpace(configTOML) == "" {
		return errors.New("tunnelcore: configuration is empty")
	}
	var cfg config.Config
	if err := toml.Unmarshal([]byte(configTOML), &cfg); err != nil {
		return err
	}
	if _, err := cfg.ActiveProfile(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.toml = configTOML
	m.cfg = &cfg
	return nil
}

func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil {
		return errors.New("tunnelcore: configuration is not loaded")
	}
	if m.packet == nil {
		ch, err := activeChain(m.cfg)
		if err != nil {
			return err
		}
		packet, err := newPacketTunnel(ch)
		if err != nil {
			return err
		}
		m.packet = packet
	}
	if !m.running {
		m.started = time.Now()
	}
	m.running = true
	return nil
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.packet != nil {
		m.packet.Close()
		m.packet = nil
	}
	m.running = false
	m.started = time.Time{}
}

func (m *Manager) InjectPacket(packet []byte) error {
	m.mu.Lock()
	tunnel := m.packet
	m.mu.Unlock()
	if tunnel == nil {
		return errors.New("tunnelcore: packet tunnel is not running")
	}
	return tunnel.InjectPacket(packet)
}

func (m *Manager) ReadPacket(timeoutMillis int) ([]byte, error) {
	m.mu.Lock()
	tunnel := m.packet
	m.mu.Unlock()
	if tunnel == nil {
		return nil, errors.New("tunnelcore: packet tunnel is not running")
	}
	return tunnel.ReadPacket(timeoutMillis)
}

func (m *Manager) ConfigTOML() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.toml
}

func (m *Manager) ProfileNamesJSON() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil {
		return "[]"
	}
	return mustJSON(m.cfg.ProfileNames())
}

func (m *Manager) StatusJSON() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	payload := statusPayload{Running: m.running}
	if m.cfg != nil {
		payload.ActiveProfile = m.cfg.Active
		payload.Profiles = m.cfg.ProfileNames()
	}
	if !m.started.IsZero() {
		payload.StartedAtUnix = m.started.Unix()
	}
	return mustJSON(payload)
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func activeChain(cfg *config.Config) (*chain.Chain, error) {
	profile, err := cfg.ActiveProfile()
	if err != nil {
		return nil, err
	}
	if len(profile.Chains) == 0 {
		return nil, errors.New("tunnelcore: active profile has no chains")
	}
	return chainFromConfig(profile.Chains[0]), nil
}

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
