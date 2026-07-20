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
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/listener"
	"github.com/JohnThre/clambhook/internal/netwatch"
	"github.com/JohnThre/clambhook/internal/policy"
	"github.com/JohnThre/clambhook/internal/procattr"
	"github.com/JohnThre/clambhook/internal/prompt"
	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/internal/rules"
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
		Profiles: []config.Profile{lifecycleProfile("A", "127.0.0.1:0", id)},
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
		Profiles: []config.Profile{lifecycleProfile("A", "127.0.0.1:0", oldID)},
	}
	e := New(cfg, nil)
	t.Cleanup(func() { _ = e.Stop() })

	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	dialFirstEngineChain(t, e)

	next := &config.Config{
		Active:   "B",
		Profiles: []config.Profile{lifecycleProfile("B", "127.0.0.1:0", newID)},
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
			lifecycleProfile("A", "127.0.0.1:0", oldID),
			lifecycleProfile("B", "127.0.0.1:0", newID),
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
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold port: %v", err)
	}
	defer held.Close()
	addrA := freePort(t)

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
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold port: %v", err)
	}
	defer held.Close()
	addrA := freePort(t)

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

	resolver := newChainResolver(&profile, "", nil, nil)
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
	if plan.RouteControl.Mode != "rule" || plan.RouteControl.Decision != "proxy" || plan.RouteControl.PolicyGroup != "auto" || plan.RouteControl.SelectedChain != "backup" || plan.RouteControl.SelectionReason != "lowest_latency" || plan.RouteControl.Fallback {
		t.Fatalf("route control = %+v, want rule proxy auto backup lowest_latency without fallback", plan.RouteControl)
	}
}

func TestRoutePlannerPromptGateBlocks(t *testing.T) {
	profile := lifecycleProfile(t.Name(), freePort(t), t.Name()+"/primary")

	resolver := newChainResolver(&profile, "", nil, nil)
	if err := resolver.ensureBuilt(); err != nil {
		t.Fatalf("ensureBuilt: %v", err)
	}
	t.Cleanup(func() { _ = closeChains(resolver.chains) })
	planner, err := resolver.routePlanner("", nil)
	if err != nil {
		t.Fatalf("routePlanner: %v", err)
	}

	// Attribute the connection to a fake process and enable prompting.
	planner.procLookup = func(_, _ string) (procattr.Process, bool) {
		return procattr.Process{PID: 7, Name: "curl", Path: "/usr/bin/curl"}, true
	}
	pm := prompt.New()
	pm.Configure(prompt.Config{Enabled: true, Timeout: 2 * time.Second})
	planner.prompts = pm

	// The user blocks the prompt shortly after it appears.
	go func() {
		deadline := time.After(2 * time.Second)
		for {
			if pend := pm.Pending(); len(pend) > 0 {
				pm.Resolve(pend[0].ID, prompt.Resolution{Allow: false})
				return
			}
			select {
			case <-deadline:
				return
			default:
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()

	plan, err := planner.PlanWithSource(context.Background(), "tcp", "example.com:443", "127.0.0.1:54321")
	if err != nil {
		t.Fatalf("PlanWithSource: %v", err)
	}
	if plan.Action != rules.ActionBlock {
		t.Fatalf("plan action = %q, want block", plan.Action)
	}
	if plan.ProcessName != "curl" || plan.ProcessPID != 7 {
		t.Fatalf("plan process = %q/%d, want curl/7", plan.ProcessName, plan.ProcessPID)
	}
	if plan.Dial != nil {
		t.Fatal("blocked plan must not carry a dialer")
	}
}

func TestRoutePlannerPromptGateAllows(t *testing.T) {
	profile := lifecycleProfile(t.Name(), freePort(t), t.Name()+"/primary")

	resolver := newChainResolver(&profile, "", nil, nil)
	if err := resolver.ensureBuilt(); err != nil {
		t.Fatalf("ensureBuilt: %v", err)
	}
	t.Cleanup(func() { _ = closeChains(resolver.chains) })
	planner, err := resolver.routePlanner("", nil)
	if err != nil {
		t.Fatalf("routePlanner: %v", err)
	}
	planner.procLookup = func(_, _ string) (procattr.Process, bool) {
		return procattr.Process{PID: 7, Name: "curl", Path: "/usr/bin/curl"}, true
	}
	pm := prompt.New()
	pm.Configure(prompt.Config{Enabled: true, Timeout: 2 * time.Second})
	planner.prompts = pm

	go func() {
		deadline := time.After(2 * time.Second)
		for {
			if pend := pm.Pending(); len(pend) > 0 {
				pm.Resolve(pend[0].ID, prompt.Resolution{Allow: true})
				return
			}
			select {
			case <-deadline:
				return
			default:
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()

	plan, err := planner.PlanWithSource(context.Background(), "tcp", "example.com:443", "127.0.0.1:54321")
	if err != nil {
		t.Fatalf("PlanWithSource: %v", err)
	}
	if plan.Action != rules.ActionChain || !plan.Default {
		t.Fatalf("allowed plan = %+v, want default chain", plan)
	}
	if plan.Dial == nil {
		t.Fatal("allowed plan must carry a dialer")
	}
}

// TestSOCKS5PromptBlocksEndToEnd drives a real SOCKS5 listener through the real
// route planner with prompting enabled and live process attribution: a client
// CONNECT is paused, attributed to this test process, then blocked once the
// prompt is resolved. This is the end-to-end smoke test for the local-proxy
// interactive-prompt path.
func TestSOCKS5PromptBlocksEndToEnd(t *testing.T) {
	profile := lifecycleProfile(t.Name(), freePort(t), t.Name()+"/primary")

	resolver := newChainResolver(&profile, "", nil, nil)
	if err := resolver.ensureBuilt(); err != nil {
		t.Fatalf("ensureBuilt: %v", err)
	}
	t.Cleanup(func() { _ = closeChains(resolver.chains) })
	planner, err := resolver.routePlanner("", nil)
	if err != nil {
		t.Fatalf("routePlanner: %v", err)
	}
	pm := prompt.New()
	pm.Configure(prompt.Config{Enabled: true, Timeout: 5 * time.Second})
	planner.prompts = pm

	s := listener.NewSOCKSv5WithPlanner("127.0.0.1:0", nil, planner, listener.Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("start socks5: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop() })

	// Resolve the prompt to block once it appears, asserting live attribution.
	attributed := make(chan string, 1)
	go func() {
		deadline := time.After(5 * time.Second)
		for {
			if pend := pm.Pending(); len(pend) > 0 {
				attributed <- pend[0].ProcessName
				pm.Resolve(pend[0].ID, prompt.Resolution{Allow: false})
				return
			}
			select {
			case <-deadline:
				attributed <- ""
				return
			default:
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()

	client, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()

	// SOCKS5 no-auth greeting.
	if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	sel := make([]byte, 2)
	if _, err := io.ReadFull(client, sel); err != nil {
		t.Fatal(err)
	}
	if sel[0] != 0x05 || sel[1] != 0x00 {
		t.Fatalf("method selection = %v, want [5,0]", sel)
	}

	// CONNECT example.com:80 — no rule matches, so the prompt gate fires.
	req := append([]byte{0x05, 0x01, 0x00, 0x03, 11}, []byte("example.com")...)
	req = append(req, 0x00, 0x50)
	if _, err := client.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if reply[0] != 0x05 {
		t.Fatalf("reply version = %d, want 5", reply[0])
	}
	if reply[1] == 0x00 {
		t.Fatal("blocked CONNECT returned success reply; gate did not block")
	}

	name := <-attributed
	if name == "" {
		t.Skip("live process attribution unavailable in this sandbox; gate still blocked the connection")
	}
}

// TestSOCKS5PromptDefaultAllowAfterTimeoutEndToEnd proves that when a prompt
// times out with DefaultAllow=true, the SOCKS5 handler waits out the prompt on
// the handler lifetime (not the 30s dial budget) and then dials with a fresh
// budget, letting the CONNECT succeed. A prompt Timeout longer than the dial
// budget is therefore honored.
func TestSOCKS5PromptDefaultAllowAfterTimeoutEndToEnd(t *testing.T) {
	profile := lifecycleProfile(t.Name(), freePort(t), t.Name()+"/primary")

	resolver := newChainResolver(&profile, "", nil, nil)
	if err := resolver.ensureBuilt(); err != nil {
		t.Fatalf("ensureBuilt: %v", err)
	}
	t.Cleanup(func() { _ = closeChains(resolver.chains) })
	planner, err := resolver.routePlanner("", nil)
	if err != nil {
		t.Fatalf("routePlanner: %v", err)
	}
	// Force attribution so the prompt gate reliably fires regardless of the
	// sandbox's live process-lookup support.
	planner.procLookup = func(_, _ string) (procattr.Process, bool) {
		return procattr.Process{PID: 7, Name: "curl", Path: "/usr/bin/curl"}, true
	}
	pm := prompt.New()
	pm.Configure(prompt.Config{Enabled: true, Timeout: 31 * time.Second, DefaultAllow: true})
	planner.prompts = pm

	s := listener.NewSOCKSv5WithPlanner("127.0.0.1:0", nil, planner, listener.Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("start socks5: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop() })

	// Confirm the prompt actually pauses the connection before the default
	// allow is applied.
	promptSeen := make(chan struct{}, 1)
	go func() {
		deadline := time.After(2 * time.Second)
		for {
			if len(pm.Pending()) > 0 {
				promptSeen <- struct{}{}
				return
			}
			select {
			case <-deadline:
				return
			default:
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()

	client, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()
	if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	sel := make([]byte, 2)
	if _, err := io.ReadFull(client, sel); err != nil {
		t.Fatal(err)
	}
	req := append([]byte{0x05, 0x01, 0x00, 0x03, 11}, []byte("example.com")...)
	req = append(req, 0x00, 0x50)
	if _, err := client.Write(req); err != nil {
		t.Fatal(err)
	}

	select {
	case <-promptSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("prompt never appeared; connection was not paused")
	}

	reply := make([]byte, 10)
	_ = client.SetReadDeadline(time.Now().Add(35 * time.Second))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if reply[0] != 0x05 {
		t.Fatalf("reply version = %d, want 5", reply[0])
	}
	if reply[1] != 0x00 {
		t.Fatalf("reply = %#x, want success; DefaultAllow after prompt timeout not honored", reply[1])
	}
}

// triggerProfile builds a fixedPortProfile that auto-switches when the given
// interface is observed.
func triggerProfile(name, socksAddr, iface string) config.Profile {
	p := fixedPortProfile(name, socksAddr)
	p.NetworkTriggers = []config.NetworkTriggerConfig{{Interface: iface}}
	return p
}

// TestEngineNetworkObservationFirstMatchWins proves that a single network
// observation selects at most one profile: when two profiles match the same
// network, the first in config order wins, exactly one switch event fires,
// and the event's OldProfile is the pre-switch active profile.
func TestEngineNetworkObservationFirstMatchWins(t *testing.T) {
	baseAddr := freePort(t)
	firstAddr := freePort(t)
	secondAddr := freePort(t)

	cfg := &config.Config{
		Active: "base",
		Profiles: []config.Profile{
			fixedPortProfile("base", baseAddr),
			triggerProfile("first", firstAddr, "en0"),
			triggerProfile("second", secondAddr, "en0"),
		},
	}

	bus := events.NewBus(events.Config{SubBufferSize: 64, RingCapacity: 64, MeterInterval: time.Hour})
	defer bus.Close()
	sub := bus.Subscribe(events.Filter{Types: []string{events.TypeProfileNetworkSwitch}})
	defer sub.Unsubscribe()

	e := New(cfg, bus)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer e.Stop()

	e.handleNetworkObservation(netwatch.NetworkInfo{InterfaceName: "en0"})

	select {
	case ev := <-sub.Ch():
		data, ok := ev.Data.(events.ProfileNetworkSwitchData)
		if !ok {
			t.Fatalf("event data type = %T, want ProfileNetworkSwitchData", ev.Data)
		}
		if data.NewProfile != "first" {
			t.Errorf("NewProfile = %q, want %q (first config-order match wins)", data.NewProfile, "first")
		}
		if data.OldProfile != "base" {
			t.Errorf("OldProfile = %q, want %q (pre-switch active profile)", data.OldProfile, "base")
		}
		if data.TriggerIface != "en0" {
			t.Errorf("TriggerIface = %q, want %q", data.TriggerIface, "en0")
		}
	case <-time.After(time.Second):
		t.Fatal("expected exactly one ProfileNetworkSwitch event, got none")
	}

	// A single observation must not cascade into a second switch/event, even
	// though "second" also matches en0.
	select {
	case ev := <-sub.Ch():
		t.Fatalf("unexpected second switch event: %+v", ev.Data)
	case <-time.After(150 * time.Millisecond):
	}

	if status := e.Status(); status.Profile != "first" {
		t.Errorf("status.Profile = %q, want %q", status.Profile, "first")
	}
	if len(cfg.Profiles) != 3 {
		t.Fatalf("config profiles mutated: %+v", cfg.Profiles)
	}
}

// TestEngineNetworkObservationAlreadyActiveNoSwitch proves that when the
// first matching profile is already active, the observation is a no-op:
// no switch event and no listener rebuild.
func TestEngineNetworkObservationAlreadyActiveNoSwitch(t *testing.T) {
	activeAddr := freePort(t)
	otherAddr := freePort(t)

	cfg := &config.Config{
		Active: "active",
		Profiles: []config.Profile{
			triggerProfile("active", activeAddr, "en0"),
			triggerProfile("other", otherAddr, "en0"),
		},
	}

	bus := events.NewBus(events.Config{SubBufferSize: 64, RingCapacity: 64, MeterInterval: time.Hour})
	defer bus.Close()
	sub := bus.Subscribe(events.Filter{Types: []string{events.TypeProfileNetworkSwitch}})
	defer sub.Unsubscribe()

	e := New(cfg, bus)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer e.Stop()

	e.handleNetworkObservation(netwatch.NetworkInfo{InterfaceName: "en0"})

	select {
	case ev := <-sub.Ch():
		t.Fatalf("unexpected switch event when first match already active: %+v", ev.Data)
	case <-time.After(150 * time.Millisecond):
	}

	if status := e.Status(); status.Profile != "active" {
		t.Errorf("status.Profile = %q, want %q", status.Profile, "active")
	}
}
