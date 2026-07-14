// Package prompt implements Little Snitch-style interactive connection
// prompts. When enabled, a connection that no existing rule already decides is
// paused while the owning process is surfaced for an allow/block decision.
//
// The gate mirrors the developer-mode HTTP breakpoint pattern: the connection
// handler goroutine blocks on a channel with a timeout default, while the UI
// lists pending prompts and posts a resolution. Multiple connections sharing
// the same (profile, process, network, destination) key coalesce onto one
// pending prompt so a burst of connections produces a single question.
package prompt

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JohnThre/clambhook/internal/procattr"
)

// defaultTimeout bounds how long a connection is held awaiting a decision.
const defaultTimeout = 30 * time.Second

// Event kinds passed to the event hook.
const (
	EventPending  = "pending"
	EventResolved = "resolved"
)

// Config controls prompting behavior. It is applied atomically via Configure.
type Config struct {
	Enabled      bool
	Timeout      time.Duration
	DefaultAllow bool
}

// Request describes a connection awaiting a decision.
type Request struct {
	ConnID  string
	Profile string
	Network string
	Target  string
	Host    string
	Port    string
	Process procattr.Process
}

// Decision is the outcome applied to a connection.
type Decision struct {
	Allow bool
}

// Resolution is a user's answer to a pending prompt.
type Resolution struct {
	Allow bool
}

// Pending is the API/UI-visible form of a paused prompt.
type Pending struct {
	ID          string    `json:"id"`
	ConnID      string    `json:"conn_id,omitempty"`
	Profile     string    `json:"profile,omitempty"`
	Network     string    `json:"network,omitempty"`
	Target      string    `json:"target"`
	TargetHost  string    `json:"target_host,omitempty"`
	TargetPort  string    `json:"target_port,omitempty"`
	PID         int       `json:"pid,omitempty"`
	ProcessName string    `json:"process_name,omitempty"`
	ProcessPath string    `json:"process_path,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	Waiters     int       `json:"waiters"`
}

// EventHook is notified when a prompt is created or resolved. allow is
// meaningful only for EventResolved.
type EventHook func(kind string, p Pending, allow bool)

type pending struct {
	Pending
	key      string
	done     chan struct{}
	timer    *time.Timer
	resolved bool
	decision Decision
}

// Manager owns pending prompts and prompting configuration.
type Manager struct {
	mu      sync.RWMutex
	cfg     Config
	pending map[string]*pending
	byKey   map[string]*pending
	hook    EventHook
	nextID  atomic.Uint64
}

// New creates a disabled prompt manager.
func New() *Manager {
	return &Manager{
		pending: make(map[string]*pending),
		byKey:   make(map[string]*pending),
	}
}

// Configure applies prompting settings. A non-positive timeout uses the
// built-in default.
func (m *Manager) Configure(cfg Config) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
}

// Enabled reports whether interactive prompting is active.
func (m *Manager) Enabled() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Enabled
}

// SetEventHook installs the callback used to publish pending/resolved events.
func (m *Manager) SetEventHook(hook EventHook) {
	m.mu.Lock()
	m.hook = hook
	m.mu.Unlock()
}

// Await pauses a connection until a decision is made, the prompt times out, or
// ctx is cancelled. gated is false when prompting is disabled — the caller then
// proceeds with its normal routing decision.
func (m *Manager) Await(ctx context.Context, req Request) (decision Decision, gated bool) {
	if m == nil {
		return Decision{}, false
	}
	m.mu.Lock()
	if !m.cfg.Enabled {
		m.mu.Unlock()
		return Decision{}, false
	}
	key := promptKey(req)
	p := m.byKey[key]
	newPrompt := p == nil
	if newPrompt {
		p = &pending{
			Pending: Pending{
				ID:          fmt.Sprintf("prompt-%d", m.nextID.Add(1)),
				ConnID:      req.ConnID,
				Profile:     req.Profile,
				Network:     req.Network,
				Target:      req.Target,
				TargetHost:  req.Host,
				TargetPort:  req.Port,
				PID:         req.Process.PID,
				ProcessName: req.Process.Name,
				ProcessPath: req.Process.Path,
				CreatedAt:   time.Now(),
			},
			key:  key,
			done: make(chan struct{}),
		}
		m.pending[p.ID] = p
		m.byKey[key] = p
		timeout := m.cfg.Timeout
		defaultAllow := m.cfg.DefaultAllow
		id := p.ID
		p.timer = time.AfterFunc(timeout, func() {
			m.resolve(id, Resolution{Allow: defaultAllow})
		})
	}
	p.Waiters++
	done := p.done
	hook := m.hook
	snapshot := p.Pending
	m.mu.Unlock()

	if newPrompt && hook != nil {
		hook(EventPending, snapshot, false)
	}

	select {
	case <-done:
		return p.decision, true
	case <-ctx.Done():
		return Decision{Allow: false}, true
	}
}

// Pending returns current pending prompts, newest first.
func (m *Manager) Pending() []Pending {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Pending, 0, len(m.pending))
	for _, p := range m.pending {
		out = append(out, p.Pending)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// Resolve applies a user decision to a pending prompt, waking every coalesced
// waiter. It returns the resolved prompt's snapshot so callers can build a
// remembered rule from the attributed process and destination.
func (m *Manager) Resolve(id string, res Resolution) (Pending, bool) {
	return m.resolve(id, res)
}

func (m *Manager) resolve(id string, res Resolution) (Pending, bool) {
	if m == nil {
		return Pending{}, false
	}
	m.mu.Lock()
	p := m.pending[id]
	if p == nil || p.resolved {
		m.mu.Unlock()
		return Pending{}, false
	}
	p.resolved = true
	p.decision = Decision(res)
	delete(m.pending, id)
	if m.byKey[p.key] == p {
		delete(m.byKey, p.key)
	}
	if p.timer != nil {
		p.timer.Stop()
	}
	snapshot := p.Pending
	hook := m.hook
	close(p.done)
	m.mu.Unlock()

	if hook != nil {
		hook(EventResolved, snapshot, res.Allow)
	}
	return snapshot, true
}

// promptKey coalesces connections from the same process to the same target so
// a burst produces one prompt. Missing process attribution falls back to the
// source connection so unattributable flows still prompt individually.
func promptKey(req Request) string {
	proc := req.Process.Path
	if proc == "" {
		proc = req.Process.Name
	}
	if proc == "" {
		proc = "pid:" + fmt.Sprint(req.Process.PID)
	}
	return req.Profile + "\x00" + proc + "\x00" + req.Network + "\x00" + req.Host + "\x00" + req.Port
}
