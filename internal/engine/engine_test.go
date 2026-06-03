package engine

import (
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/policy"
	"github.com/JohnThre/clambhook/internal/protocol"
)

var engineLifecycleState = newEngineLifecycleState()

type engineLifecycleStateStore struct {
	mu        sync.Mutex
	factories map[string]int
	closes    map[string]int
}

func newEngineLifecycleState() *engineLifecycleStateStore {
	return &engineLifecycleStateStore{
		factories: map[string]int{},
		closes:    map[string]int{},
	}
}

func (s *engineLifecycleStateStore) reset(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.factories, id)
	delete(s.closes, id)
}

func (s *engineLifecycleStateStore) recordFactory(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.factories[id]++
}

func (s *engineLifecycleStateStore) recordClose(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closes[id]++
}

func (s *engineLifecycleStateStore) closeCount(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closes[id]
}

func (s *engineLifecycleStateStore) factoryCount(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.factories[id]
}

type engineLifecycleDialer struct {
	id string
}

func (d *engineLifecycleDialer) Protocol() string { return "engine_lifecycle" }

func (d *engineLifecycleDialer) Dial(_ context.Context, _ string, _ string) (protocol.Conn, error) {
	client, server := net.Pipe()
	go func() {
		_, _ = io.Copy(io.Discard, server)
		_ = server.Close()
	}()
	return &engineLifecycleConn{Conn: client}, nil
}

func (d *engineLifecycleDialer) DialThrough(_ context.Context, underlying io.ReadWriteCloser, _ string) (protocol.Conn, error) {
	if underlying != nil {
		_ = underlying.Close()
	}
	return nil, io.ErrClosedPipe
}

func (d *engineLifecycleDialer) Close() error {
	engineLifecycleState.recordClose(d.id)
	return nil
}

type engineLifecycleConn struct {
	net.Conn
}

func (c *engineLifecycleConn) Protocol() string { return "engine_lifecycle" }

func init() {
	protocol.Register("engine_lifecycle", func(s protocol.Server) (protocol.Dialer, error) {
		id, _ := s.Settings["id"].(string)
		engineLifecycleState.recordFactory(id)
		return &engineLifecycleDialer{id: id}, nil
	})
}

// fixedPortProfile returns a minimal profile with one chain and the given
// SOCKS5 listen address.
func fixedPortProfile(name, socksAddr string) config.Profile {
	return config.Profile{
		Name:   name,
		Listen: config.ListenConfig{SOCKS5: socksAddr},
		Chains: []config.ChainConfig{{
			Name: "default",
			Servers: []config.ServerConfig{{
				Name:     "dummy",
				Address:  "127.0.0.1:1",
				Protocol: "engine_lifecycle",
				Settings: map[string]any{"id": name + "/fixed"},
			}},
		}},
	}
}

// freePort returns a port number that was briefly bound then released —
// good enough for a local test where the port is re-bound within
// milliseconds.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestEngineSetActiveProfileRebuildsListeners(t *testing.T) {
	addrA := freePort(t)
	addrB := freePort(t)

	cfg := &config.Config{
		Active: "A",
		Profiles: []config.Profile{
			fixedPortProfile("A", addrA),
			fixedPortProfile("B", addrB),
		},
	}

	e := New(cfg, nil)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer e.Stop()

	// Port A should be bound.
	if _, err := net.Listen("tcp", addrA); err == nil {
		t.Errorf("expected %s already bound, but we took it", addrA)
	}

	if err := e.SetActiveProfile("B"); err != nil {
		t.Fatalf("switch: %v", err)
	}

	// Port A should be free now.
	la, err := net.Listen("tcp", addrA)
	if err != nil {
		t.Errorf("expected %s released after profile switch: %v", addrA, err)
	} else {
		la.Close()
	}

	// Port B should be bound.
	if _, err := net.Listen("tcp", addrB); err == nil {
		t.Errorf("expected %s bound after profile switch", addrB)
	}

	// Status reflects the new profile.
	status := e.Status()
	if status.Profile != "B" {
		t.Errorf("status.Profile = %q, want %q", status.Profile, "B")
	}
	if len(status.Listeners) != 1 || status.Listeners[0].Addr != addrB {
		t.Errorf("status.Listeners = %+v, want one bound at %s", status.Listeners, addrB)
	}
}

func TestEngineSetActiveProfileNotFound(t *testing.T) {
	cfg := &config.Config{
		Active:   "A",
		Profiles: []config.Profile{fixedPortProfile("A", freePort(t))},
	}
	e := New(cfg, nil)
	if err := e.SetActiveProfile("bogus"); err == nil {
		t.Error("expected error for unknown profile")
	}
}

func TestEngineReloadIdle(t *testing.T) {
	cfg := &config.Config{
		Active:   "A",
		Profiles: []config.Profile{fixedPortProfile("A", freePort(t))},
	}
	e := New(cfg, nil)
	// Reload before Start — should just swap config without error.
	cfg2 := &config.Config{
		Active:   "B",
		Profiles: []config.Profile{fixedPortProfile("B", freePort(t))},
	}
	if err := e.Reload(cfg2); err != nil {
		t.Errorf("reload idle: %v", err)
	}
	if e.Config().Active != "B" {
		t.Error("reload did not replace config")
	}
}

func lifecycleProfile(name, socksAddr, id string) config.Profile {
	return config.Profile{
		Name:   name,
		Listen: config.ListenConfig{SOCKS5: socksAddr},
		Chains: []config.ChainConfig{{
			Name: "default",
			Servers: []config.ServerConfig{{
				Name:     "dummy",
				Address:  "127.0.0.1:1",
				Protocol: "engine_lifecycle",
				Settings: map[string]any{"id": id},
			}},
		}},
	}
}

func dialFirstEngineChain(t *testing.T, e *Engine) {
	t.Helper()
	if len(e.chains) != 1 {
		t.Fatalf("len(e.chains) = %d, want 1", len(e.chains))
	}
	ctx, cancel := context.WithTimeout(context.Background(), testDialTimeout)
	defer cancel()
	conn, err := e.chains[0].Dial(ctx, "tcp", "target.example:443")
	if err != nil {
		t.Fatalf("chain Dial: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("conn Close: %v", err)
	}
}

const testDialTimeout = 2 * time.Second

func TestEngineStopClosesCachedChainDialers(t *testing.T) {
	id := t.Name()
	engineLifecycleState.reset(id)

	cfg := &config.Config{
		Active:   "A",
		Profiles: []config.Profile{lifecycleProfile("A", freePort(t), id)},
	}
	e := New(cfg, nil)
	t.Cleanup(func() { _ = e.Stop() })

	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	dialFirstEngineChain(t, e)
	if got := engineLifecycleState.factoryCount(id); got != 2 {
		t.Fatalf("factory count = %d, want 2 (preflight + runtime)", got)
	}

	if err := e.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := engineLifecycleState.closeCount(id); got != 2 {
		t.Fatalf("close count = %d, want 2 (preflight + runtime)", got)
	}
	if err := e.Stop(); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	if got := engineLifecycleState.closeCount(id); got != 2 {
		t.Fatalf("close count after second stop = %d, want 2", got)
	}
}

func TestEngineReloadClosesOldCachedChainDialers(t *testing.T) {
	oldID := t.Name() + "/old"
	newID := t.Name() + "/new"
	engineLifecycleState.reset(oldID)
	engineLifecycleState.reset(newID)

	cfg := &config.Config{
		Active:   "A",
		Profiles: []config.Profile{lifecycleProfile("A", freePort(t), oldID)},
	}
	e := New(cfg, nil)
	t.Cleanup(func() { _ = e.Stop() })

	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	dialFirstEngineChain(t, e)

	next := &config.Config{
		Active:   "B",
		Profiles: []config.Profile{lifecycleProfile("B", freePort(t), newID)},
	}
	if err := e.Reload(next); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := engineLifecycleState.closeCount(oldID); got != 2 {
		t.Fatalf("old close count = %d, want 2 (preflight + runtime)", got)
	}

	dialFirstEngineChain(t, e)
	if got := engineLifecycleState.factoryCount(newID); got != 2 {
		t.Fatalf("new factory count = %d, want 2 (preflight + runtime)", got)
	}
	if err := e.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := engineLifecycleState.closeCount(newID); got != 2 {
		t.Fatalf("new close count = %d, want 2", got)
	}
}

func TestEngineSetActiveProfileClosesOldCachedChainDialers(t *testing.T) {
	oldID := t.Name() + "/old"
	newID := t.Name() + "/new"
	engineLifecycleState.reset(oldID)
	engineLifecycleState.reset(newID)

	cfg := &config.Config{
		Active: "A",
		Profiles: []config.Profile{
			lifecycleProfile("A", freePort(t), oldID),
			lifecycleProfile("B", freePort(t), newID),
		},
	}
	e := New(cfg, nil)
	t.Cleanup(func() { _ = e.Stop() })

	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	dialFirstEngineChain(t, e)

	if err := e.SetActiveProfile("B"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if got := engineLifecycleState.closeCount(oldID); got != 2 {
		t.Fatalf("old close count = %d, want 2 (preflight + runtime)", got)
	}

	dialFirstEngineChain(t, e)
	if err := e.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := engineLifecycleState.closeCount(newID); got != 2 {
		t.Fatalf("new close count = %d, want 2", got)
	}
}

func TestEngineReloadValidationFailureKeepsOldListenerRunning(t *testing.T) {
	addrA := freePort(t)
	addrB := freePort(t)
	cfg := &config.Config{
		Active:   "A",
		Profiles: []config.Profile{fixedPortProfile("A", addrA)},
	}
	e := New(cfg, nil)
	t.Cleanup(func() { _ = e.Stop() })

	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	nextProfile := fixedPortProfile("B", addrB)
	nextProfile.Chains[0].Servers[0].Protocol = "missing_protocol"
	err := e.Reload(&config.Config{Active: "B", Profiles: []config.Profile{nextProfile}})
	if err == nil {
		t.Fatal("Reload returned nil, want validation error")
	}

	if _, err := net.Listen("tcp", addrA); err == nil {
		t.Fatalf("expected old listener %s to remain bound after failed reload", addrA)
	}
	if status := e.Status(); status.Profile != "A" || !status.Running {
		t.Fatalf("status after failed reload = %+v, want running profile A", status)
	}
}

func TestEngineReloadStartFailureRollsBackOldListener(t *testing.T) {
	addrA := freePort(t)
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold port: %v", err)
	}
	defer held.Close()

	cfg := &config.Config{
		Active:   "A",
		Profiles: []config.Profile{fixedPortProfile("A", addrA)},
	}
	e := New(cfg, nil)
	t.Cleanup(func() { _ = e.Stop() })

	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	next := &config.Config{
		Active:   "B",
		Profiles: []config.Profile{fixedPortProfile("B", held.Addr().String())},
	}
	err = e.Reload(next)
	if err == nil || !strings.Contains(err.Error(), "rolled back to previous config") {
		t.Fatalf("Reload error = %v, want rollback message", err)
	}
	if _, err := net.Listen("tcp", addrA); err == nil {
		t.Fatalf("expected old listener %s to be rebound after rollback", addrA)
	}
	if status := e.Status(); status.Profile != "A" || !status.Running {
		t.Fatalf("status after rollback = %+v, want running profile A", status)
	}
}

func TestEngineSetActiveProfileStartFailureRollsBackOldProfile(t *testing.T) {
	addrA := freePort(t)
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold port: %v", err)
	}
	defer held.Close()

	cfg := &config.Config{
		Active: "A",
		Profiles: []config.Profile{
			fixedPortProfile("A", addrA),
			fixedPortProfile("B", held.Addr().String()),
		},
	}
	e := New(cfg, nil)
	t.Cleanup(func() { _ = e.Stop() })

	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	err = e.SetActiveProfile("B")
	if err == nil || !strings.Contains(err.Error(), `rolled back to profile "A"`) {
		t.Fatalf("SetActiveProfile error = %v, want rollback message", err)
	}
	if _, err := net.Listen("tcp", addrA); err == nil {
		t.Fatalf("expected old listener %s to be rebound after rollback", addrA)
	}
	if status := e.Status(); status.Profile != "A" || !status.Running {
		t.Fatalf("status after rollback = %+v, want running profile A", status)
	}
}

func TestBuildListenersSharesRuntimeChainAcrossListeners(t *testing.T) {
	profile := lifecycleProfile(t.Name(), freePort(t), t.Name()+"/chain")
	profile.Listen.HTTP = freePort(t)

	listeners, chains, policies, err := buildListeners(&profile, nil)
	if err != nil {
		t.Fatalf("buildListeners: %v", err)
	}
	t.Cleanup(func() { _ = policies.Close() })
	t.Cleanup(func() { _ = closeChains(chains) })
	if len(listeners) != 2 {
		t.Fatalf("len(listeners) = %d, want 2", len(listeners))
	}
	if len(chains) != 1 {
		t.Fatalf("len(chains) = %d, want 1", len(chains))
	}
}

func TestRoutePlannerResolvesPolicyGroupSelection(t *testing.T) {
	profile := lifecycleProfile(t.Name(), freePort(t), t.Name()+"/primary")
	profile.Chains[0].Name = "primary"
	profile.Chains = append(profile.Chains, config.ChainConfig{
		Name: "backup",
		Servers: []config.ServerConfig{{
			Name:     "dummy",
			Address:  "127.0.0.1:1",
			Protocol: "engine_lifecycle",
			Settings: map[string]any{"id": t.Name() + "/backup"},
		}},
	})
	profile.PolicyGroups = []config.PolicyGroupConfig{{
		Name:    "auto",
		Type:    policy.TypeURLTest,
		Chains:  []string{"primary", "backup"},
		TestURL: "https://probe.example/generate_204",
	}}
	profile.Rules = []config.RuleConfig{{
		Name:   "auto",
		Action: "group:auto",
	}}

	resolver := newChainResolver(&profile)
	if err := resolver.ensureBuilt(); err != nil {
		t.Fatalf("ensureBuilt: %v", err)
	}
	t.Cleanup(func() { _ = closeChains(resolver.chains) })
	policies, err := policy.New(profile.PolicyGroups, resolver.byName, policy.WithProbeFunc(func(_ context.Context, ch *chain.Chain, _ string) policy.ProbeResult {
		if ch.Name == "backup" {
			return policy.ProbeResult{Healthy: true, LatencyNs: int64(time.Millisecond)}
		}
		return policy.ProbeResult{Healthy: true, LatencyNs: int64(50 * time.Millisecond)}
	}))
	if err != nil {
		t.Fatalf("policy.New: %v", err)
	}
	t.Cleanup(func() { _ = policies.Close() })
	if _, err := policies.Refresh(context.Background(), "auto"); err != nil {
		t.Fatalf("policy refresh: %v", err)
	}
	planner, err := resolver.routePlanner("primary", policies)
	if err != nil {
		t.Fatalf("routePlanner: %v", err)
	}

	plan, err := planner.Plan(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Action != "group" || plan.GroupName != "auto" || plan.ChainName != "backup" {
		t.Fatalf("plan = %+v, want group auto selected backup", plan)
	}
}
