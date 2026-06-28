package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/dnsproxy"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/geo"
	"github.com/JohnThre/clambhook/internal/listener"
	"github.com/JohnThre/clambhook/internal/netwatch"
	"github.com/JohnThre/clambhook/internal/policy"
	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/internal/rules"
	"github.com/JohnThre/clambhook/internal/ruleset"
	"github.com/JohnThre/clambhook/internal/subscription"
	"github.com/JohnThre/clambhook/internal/temprules"
)

// defaultSOCKS5MaxConns is the default concurrent-handler ceiling when the
// profile doesn't set socks5_max_connections. Generous for personal use;
// bounds the blast radius of a runaway client.
const defaultSOCKS5MaxConns = 512

// defaultHTTPMaxConns mirrors defaultSOCKS5MaxConns for the HTTP listener.
const defaultHTTPMaxConns = 512

type protocolDialerCloser interface {
	Close() error
}

// Status represents the engine's current state.
type Status struct {
	Running    bool             `json:"running"`
	Profile    string           `json:"profile"`
	Listeners  []ListenerStatus `json:"listeners,omitempty"`
	TunnelMode string           `json:"tunnel_mode,omitempty"`
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
	chains    []*chain.Chain
	policies  *policy.Manager
	geoReader *geo.Reader
	bus       *events.Bus
	inspector listener.HTTPInspector
	tempRules *temprules.Manager
	watcher   *netwatch.Watcher
	// currentNetwork tracks the last observed network for status reporting.
	currentNetwork netwatch.NetworkInfo
}

// New creates a new engine with the given configuration and (optional)
// event bus. The bus is threaded into each listener so per-connection
// lifecycle and bandwidth events flow out to WS subscribers. Pass nil to
// disable events (useful in tests).
//
// If a geo database is configured but fails to open, the error is logged
// and geo stays disabled — a bad geo path must never prevent the daemon
// from starting.
func New(cfg *config.Config, bus *events.Bus) *Engine {
	e := &Engine{cfg: cfg, bus: bus, tempRules: temprules.New(), watcher: netwatch.New()}
	if r, err := geo.Open(cfg.Geo.Database); err != nil {
		log.Printf("geo: %v; continuing without geo lookups", err)
	} else if r != nil {
		log.Printf("geo: opened %q", cfg.Geo.Database)
		e.geoReader = r
	}
	return e
}

// Bus returns the engine's event bus (may be nil).
func (e *Engine) Bus() *events.Bus { return e.bus }

// TemporaryRules returns the in-memory temporary-rule manager.
func (e *Engine) TemporaryRules() *temprules.Manager { return e.tempRules }

// SetTemporaryRules replaces the in-memory temporary-rule manager. It is used
// by embedded runtimes that need multiple route planners to share one rule set.
func (e *Engine) SetTemporaryRules(manager *temprules.Manager) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tempRules = manager
}

// SetHTTPInspector wires the optional developer-mode HTTP inspector. Callers
// should set it before Start or before a Reload-triggered listener rebuild.
func (e *Engine) SetHTTPInspector(inspector listener.HTTPInspector) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inspector = inspector
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
	if err := validateRuntimeConfig(e.cfg); err != nil {
		return fmt.Errorf("start engine: validate: %w", err)
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

	if err := validateRuntimeConfig(cfg); err != nil {
		return fmt.Errorf("reload: validate: %w", err)
	}

	oldCfg := e.cfg
	oldGeoPath := e.cfg.Geo.Database
	if !e.running {
		e.cfg = cfg
		if cfg.Geo.Database != oldGeoPath {
			e.swapGeoLocked()
		}
		log.Printf("engine configuration reloaded (idle)")
		return nil
	}

	if err := e.stopLocked(); err != nil {
		log.Printf("reload: stop listeners: %v", err)
	}
	e.cfg = cfg
	if err := e.startLocked(); err != nil {
		startErr := err
		e.cfg = oldCfg
		if rollbackErr := e.startLocked(); rollbackErr != nil {
			return fmt.Errorf("reload: restart: %w; rollback failed: %v", startErr, rollbackErr)
		}
		return fmt.Errorf("reload: restart: %w; rolled back to previous config", startErr)
	}
	if cfg.Geo.Database != oldGeoPath {
		e.swapGeoLocked()
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
	next := *e.cfg
	next.Active = name
	if err := validateRuntimeConfig(&next); err != nil {
		return fmt.Errorf("profile switch: validate: %w", err)
	}
	oldActive := e.cfg.Active
	e.cfg.Active = name
	if !e.running {
		return nil
	}
	if err := e.stopLocked(); err != nil {
		log.Printf("profile switch: stop listeners: %v", err)
	}
	if err := e.startLocked(); err != nil {
		startErr := err
		e.cfg.Active = oldActive
		if rollbackErr := e.startLocked(); rollbackErr != nil {
			return fmt.Errorf("profile switch: restart: %w; rollback failed: %v", startErr, rollbackErr)
		}
		return fmt.Errorf("profile switch: restart: %w; rolled back to profile %q", startErr, oldActive)
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
	effectiveProfile := subscription.ProfileWithCachedRules(e.cfg.Path, profile)

	// Engine owns its own context — independent of any caller's ctx. This
	// lets short-lived callers (HTTP handlers, CLI one-shots) invoke Start
	// without their ctx cancellation tearing listeners down.
	ctx, cancel := context.WithCancel(context.Background())

	listeners, chains, policies, err := buildListenersWithInspectorAndPath(&effectiveProfile, e.cfg.Path, e.bus, e.inspector, e.tempRules)
	if err != nil {
		cancel()
		return fmt.Errorf("start engine: %w", err)
	}

	for _, l := range listeners {
		if startErr := l.Start(ctx); startErr != nil {
			// Roll back all constructed listeners. Unstarted listeners are
			// expected to no-op but may still own prebuilt resources.
			for j := 0; j < len(listeners); j++ {
				if stopErr := listeners[j].Stop(); stopErr != nil {
					log.Printf("engine: rollback stop %s: %v", listeners[j].Protocol(), stopErr)
				}
			}
			if closeErr := closeChains(chains); closeErr != nil {
				log.Printf("engine: rollback close chains: %v", closeErr)
			}
			if policies != nil {
				_ = policies.Close()
			}
			cancel()
			return fmt.Errorf("start %s: %w", l.Protocol(), startErr)
		}
	}

	e.cancel = cancel
	e.listeners = listeners
	e.chains = chains
	e.policies = policies
	if e.policies != nil {
		e.policies.Start(ctx)
	}
	e.running = true
	if e.watcher != nil {
		go e.runNetworkWatch(ctx)
	}
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
	if e.policies != nil {
		if err := e.policies.Close(); err != nil {
			errs = append(errs, err)
		}
		e.policies = nil
	}
	if err := closeChains(e.chains); err != nil {
		errs = append(errs, err)
	}
	e.chains = nil
	e.running = false
	return errors.Join(errs...)
}

func closeChains(chains []*chain.Chain) error {
	var errs []error
	for _, ch := range chains {
		if ch == nil {
			continue
		}
		if err := ch.Close(); err != nil {
			errs = append(errs, fmt.Errorf("chain %q: %w", ch.Name, err))
		}
	}
	return errors.Join(errs...)
}

func validateRuntimeConfig(cfg *config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	profile, err := cfg.ActiveProfile()
	if err != nil {
		return err
	}

	var errs []error
	for chainIdx, ch := range profile.Chains {
		for serverIdx, server := range ch.Servers {
			dialer, err := protocol.NewDialer(protocol.Server{
				Name:     server.Name,
				Address:  server.Address,
				Protocol: server.Protocol,
				Settings: server.Settings,
			})
			if err != nil {
				errs = append(errs, fmt.Errorf("profile %q chain %q server %d protocol %q: %w",
					profile.Name, ch.Name, serverIdx, server.Protocol, err))
				continue
			}
			closer, ok := dialer.(protocolDialerCloser)
			if !ok || closer == nil {
				continue
			}
			if err := closer.Close(); err != nil {
				errs = append(errs, fmt.Errorf("profile %q chain %d server %d close preflight dialer: %w",
					profile.Name, chainIdx, serverIdx, err))
			}
		}
	}
	return errors.Join(errs...)
}

// ValidateConfig applies the same runtime validation used before starting the
// daemon. Embedded clients use it before starting a TUN/VPN service without
// constructing daemon listeners.
func ValidateConfig(cfg *config.Config) error {
	return validateRuntimeConfig(cfg)
}

// Status returns the engine's current status.
func (e *Engine) Status() Status {
	e.mu.RLock()
	defer e.mu.RUnlock()

	s := Status{Running: e.running}
	if profile, err := e.cfg.ActiveProfile(); err == nil {
		s.Profile = profile.Name
	}
	hasTUN := false
	for _, l := range e.listeners {
		proto := l.Protocol()
		s.Listeners = append(s.Listeners, ListenerStatus{
			Protocol:    proto,
			Addr:        l.Addr(),
			ActiveConns: l.ActiveConns(),
		})
		if strings.EqualFold(proto, "tun") {
			hasTUN = true
		}
	}
	if hasTUN {
		s.TunnelMode = "tun"
	} else if len(e.listeners) > 0 {
		s.TunnelMode = "proxy"
	}
	return s
}

// Config returns the current configuration.
func (e *Engine) Config() *config.Config {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg
}

// PolicySnapshot returns active profile policy group state. When the engine is
// idle, it returns config-only defaults because no runtime probes are active.
func (e *Engine) PolicySnapshot() policy.Snapshot {
	snap, _ := e.PolicySnapshotForProfile("")
	return snap
}

// PolicySnapshotForProfile returns policy group state for the requested
// profile. The active running profile includes live probe state; inactive
// profiles and idle engines return config-only defaults.
func (e *Engine) PolicySnapshotForProfile(profileName string) (policy.Snapshot, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	profileName = strings.TrimSpace(profileName)
	var (
		profile *config.Profile
		err     error
	)
	if profileName == "" {
		profile, err = e.cfg.ActiveProfile()
	} else {
		var ok bool
		profile, ok = e.cfg.ProfileByName(profileName)
		if !ok {
			err = fmt.Errorf("profile %q not found", profileName)
		}
	}
	if err != nil {
		return policy.Snapshot{}, err
	}
	activeProfile, activeErr := e.cfg.ActiveProfile()
	if e.policies != nil && activeErr == nil && profile.Name == activeProfile.Name {
		return e.policies.Snapshot(profile.Name), nil
	}
	return policy.ConfigSnapshot(profile.Name, profile.PolicyGroups), nil
}

// RefreshPolicyGroups runs latency tests for one group or all groups.
func (e *Engine) RefreshPolicyGroups(ctx context.Context, groupName string) (policy.Snapshot, error) {
	e.mu.RLock()
	running := e.running
	manager := e.policies
	profile, profileErr := e.cfg.ActiveProfile()
	profileName := ""
	var groups []config.PolicyGroupConfig
	if profileErr == nil {
		profileName = profile.Name
		groups = append([]config.PolicyGroupConfig(nil), profile.PolicyGroups...)
	}
	e.mu.RUnlock()

	if profileErr != nil {
		return policy.Snapshot{}, profileErr
	}
	if !running || manager == nil {
		return policy.ConfigSnapshot(profileName, groups), errors.New("policy group tests require a running engine")
	}
	snap, err := manager.Refresh(ctx, groupName)
	snap.Profile = profileName
	return snap, err
}

// SelectedPolicyChain returns the currently selected concrete chain for a
// group on the active running profile. ok is false when no manager is active.
func (e *Engine) SelectedPolicyChain(groupName string, sel policy.SelectionContext) (name string, ok bool, err error) {
	e.mu.RLock()
	manager := e.policies
	running := e.running
	e.mu.RUnlock()
	if !running || manager == nil {
		return "", false, nil
	}
	_, selected, err := manager.Select(groupName, sel)
	if err != nil {
		return "", true, err
	}
	return selected, true, nil
}

// NetworkInfo returns the most recently detected network. The zero value is
// returned when netwatch has not yet reported or is not available.
func (e *Engine) NetworkInfo() netwatch.NetworkInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.currentNetwork
}

// runNetworkWatch monitors for network changes and auto-switches profiles
// when a matching NetworkTrigger is found in the config. It runs as a
// goroutine launched by startLocked and exits when ctx is cancelled.
func (e *Engine) runNetworkWatch(ctx context.Context) {
	for info := range e.watcher.Watch(ctx) {
		e.mu.Lock()
		e.currentNetwork = info
		cfg := e.cfg
		oldActive := cfg.Active
		e.mu.Unlock()

		for _, profile := range cfg.Profiles {
			if profile.Name == oldActive {
				continue
			}
			for _, trigger := range profile.NetworkTriggers {
				if !info.MatchesTrigger(trigger.SSID, trigger.Interface) {
					continue
				}
				log.Printf("netwatch: network %q (iface %q) matches trigger for profile %q; switching",
					info.SSID, info.InterfaceName, profile.Name)
				if err := e.SetActiveProfile(profile.Name); err != nil {
					log.Printf("netwatch: auto-switch to profile %q failed: %v", profile.Name, err)
					break
				}
				e.bus.PublishListener(events.TypeProfileNetworkSwitch, events.ProfileNetworkSwitchData{
					OldProfile:    oldActive,
					NewProfile:    profile.Name,
					TriggerSSID:   trigger.SSID,
					TriggerIface:  trigger.Interface,
					DetectedSSID:  info.SSID,
					DetectedIface: info.InterfaceName,
				})
				break
			}
		}
	}
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
// back cleanly. bus is threaded into each listener for event emission; may
// be nil to disable events.
func buildListeners(profile *config.Profile, bus *events.Bus) (listeners []listener.Listener, chains []*chain.Chain, policies *policy.Manager, err error) {
	return buildListenersWithInspector(profile, bus, nil)
}

func buildListenersWithInspector(profile *config.Profile, bus *events.Bus, inspector listener.HTTPInspector) (listeners []listener.Listener, chains []*chain.Chain, policies *policy.Manager, err error) {
	return buildListenersWithInspectorAndPath(profile, "", bus, inspector, nil)
}

func buildListenersWithInspectorAndPath(profile *config.Profile, configPath string, bus *events.Bus, inspector listener.HTTPInspector, tempRules *temprules.Manager) (listeners []listener.Listener, chains []*chain.Chain, policies *policy.Manager, err error) {
	var out []listener.Listener
	resolver := newChainResolver(profile, configPath, tempRules)
	defer func() {
		if err != nil {
			if policies != nil {
				if closeErr := policies.Close(); closeErr != nil {
					err = errors.Join(err, closeErr)
				}
			}
			if closeErr := closeChains(resolver.chains); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}
	}()
	if err := resolver.ensureBuilt(); err != nil {
		return nil, nil, nil, err
	}
	policies, err = policy.New(profile.PolicyGroups, resolver.byName)
	if err != nil {
		return nil, nil, nil, err
	}

	if addr := profile.Listen.SOCKS5; addr != "" {
		if _, err := resolver.resolve(profile.Listen.SOCKS5Chain); err != nil {
			return nil, nil, nil, fmt.Errorf("socks5: %w", err)
		}
		planner, err := resolver.routePlanner(profile.Listen.SOCKS5Chain, policies)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("socks5 rules: %w", err)
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
			ProfileName:      profile.Name,
			MaxConnections:   maxConns,
			HandshakeTimeout: profile.Listen.SOCKS5HandshakeTimeout.Std(),
			EventBus:         bus,
		}
		out = append(out, listener.NewSOCKSv5WithPlanner(addr, auth, planner, opts))
	}

	if addr := profile.Listen.HTTP; addr != "" {
		if _, err := resolver.resolve(profile.Listen.HTTPChain); err != nil {
			return nil, nil, nil, fmt.Errorf("http: %w", err)
		}
		planner, err := resolver.routePlanner(profile.Listen.HTTPChain, policies)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("http rules: %w", err)
		}
		var auth *listener.AuthCreds
		if profile.Listen.HTTPAuth != nil {
			auth = &listener.AuthCreds{
				Username: profile.Listen.HTTPAuth.Username,
				Password: profile.Listen.HTTPAuth.Password,
			}
		}
		maxConns := profile.Listen.HTTPMaxConns
		if maxConns == 0 {
			maxConns = defaultHTTPMaxConns
		}
		opts := listener.Options{
			ProfileName:      profile.Name,
			MaxConnections:   maxConns,
			HandshakeTimeout: profile.Listen.HTTPHandshakeTimeout.Std(),
			EventBus:         bus,
			HTTPInspector:    inspector,
		}
		out = append(out, listener.NewHTTPWithPlanner(addr, auth, planner, opts))
	}

	if tunCfg := profile.Listen.TUN; tunCfg != nil && tunCfg.Enabled {
		if !listener.TUNSupported() {
			return nil, nil, nil, listener.TUNUnsupportedError()
		}
		ch, err := resolver.resolve(tunCfg.Chain)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("tun: %w", err)
		}
		planner, err := resolver.routePlanner(tunCfg.Chain, policies)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("tun rules: %w", err)
		}
		dnsProxy, err := dnsproxy.New(profile.DNS, planner)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("tun dns: %w", err)
		}
		opts := listener.TUNOptions{
			Name:         tunCfg.Name,
			ProfileName:  profile.Name,
			MTU:          tunCfg.MTU,
			Addresses:    tunCfg.Addresses,
			Routes:       tunCfg.Routes,
			ExcludeCIDRs: tunCfg.ExcludeCIDRs,
			ChainName:    ch.Name,
			EventBus:     bus,
			DNSProxy:     dnsProxy,
		}
		out = append(out, listener.NewTUNWithPlanner(opts, ch, planner))
	}

	return out, resolver.chains, policies, nil
}

// BuildPacketStack constructs a platform-neutral packet stack for the active
// profile's TUN configuration. The caller owns Start/Stop and chain cleanup.
func BuildPacketStack(profile *config.Profile, bus *events.Bus, writer listener.PacketWriter) (*listener.PacketStack, []*chain.Chain, error) {
	return buildPacketStackWithConfigPath(profile, "", bus, writer)
}

func buildPacketStackWithConfigPath(profile *config.Profile, configPath string, bus *events.Bus, writer listener.PacketWriter) (*listener.PacketStack, []*chain.Chain, error) {
	if profile == nil {
		return nil, nil, errors.New("nil profile")
	}
	tunCfg := profile.Listen.TUN
	if tunCfg == nil || !tunCfg.Enabled {
		return nil, nil, errors.New("tun: listener is not enabled in active profile")
	}
	resolver := newChainResolver(profile, configPath, nil)
	if err := resolver.ensureBuilt(); err != nil {
		return nil, nil, err
	}
	policies, err := policy.New(profile.PolicyGroups, resolver.byName)
	if err != nil {
		if closeErr := closeChains(resolver.chains); closeErr != nil {
			return nil, nil, errors.Join(err, closeErr)
		}
		return nil, nil, err
	}
	ch, err := resolver.resolve(tunCfg.Chain)
	if err != nil {
		_ = policies.Close()
		_ = closeChains(resolver.chains)
		return nil, nil, fmt.Errorf("tun: %w", err)
	}
	planner, err := resolver.routePlanner(tunCfg.Chain, policies)
	if err != nil {
		_ = policies.Close()
		_ = closeChains(resolver.chains)
		return nil, nil, fmt.Errorf("tun rules: %w", err)
	}
	dnsProxy, err := dnsproxy.New(profile.DNS, planner)
	if err != nil {
		_ = policies.Close()
		_ = closeChains(resolver.chains)
		return nil, nil, fmt.Errorf("tun dns: %w", err)
	}
	opts := listener.TUNOptions{
		Name:          tunCfg.Name,
		ProfileName:   profile.Name,
		MTU:           tunCfg.MTU,
		Addresses:     tunCfg.Addresses,
		Routes:        tunCfg.Routes,
		ExcludeCIDRs:  tunCfg.ExcludeCIDRs,
		ChainName:     ch.Name,
		EventBus:      bus,
		DNSProxy:      dnsProxy,
		PolicyManager: policies,
	}
	return listener.NewPacketStack(opts, ch, planner, writer), resolver.chains, nil
}

// BuildPacketStackForConfig constructs a packet stack for cfg's active profile
// after applying cached subscription rules.
func BuildPacketStackForConfig(cfg *config.Config, bus *events.Bus, writer listener.PacketWriter) (*listener.PacketStack, []*chain.Chain, error) {
	return BuildPacketStackForConfigWithTemporaryRules(cfg, bus, writer, nil)
}

// BuildPacketStackForConfigWithTemporaryRules constructs a packet stack and
// evaluates temporary routing rules before the profile's persisted rules.
func BuildPacketStackForConfigWithTemporaryRules(cfg *config.Config, bus *events.Bus, writer listener.PacketWriter, tempRules *temprules.Manager) (*listener.PacketStack, []*chain.Chain, error) {
	if cfg == nil {
		return nil, nil, errors.New("nil config")
	}
	profile, err := cfg.ActiveProfile()
	if err != nil {
		return nil, nil, err
	}
	effectiveProfile := subscription.ProfileWithCachedRules(cfg.Path, profile)
	return buildPacketStackWithConfigPathAndTemporaryRules(&effectiveProfile, cfg.Path, bus, writer, tempRules)
}

func buildPacketStackWithConfigPathAndTemporaryRules(profile *config.Profile, configPath string, bus *events.Bus, writer listener.PacketWriter, tempRules *temprules.Manager) (*listener.PacketStack, []*chain.Chain, error) {
	if profile == nil {
		return nil, nil, errors.New("nil profile")
	}
	tunCfg := profile.Listen.TUN
	if tunCfg == nil || !tunCfg.Enabled {
		return nil, nil, errors.New("tun: listener is not enabled in active profile")
	}
	resolver := newChainResolver(profile, configPath, tempRules)
	if err := resolver.ensureBuilt(); err != nil {
		return nil, nil, err
	}
	policies, err := policy.New(profile.PolicyGroups, resolver.byName)
	if err != nil {
		if closeErr := closeChains(resolver.chains); closeErr != nil {
			return nil, nil, errors.Join(err, closeErr)
		}
		return nil, nil, err
	}
	ch, err := resolver.resolve(tunCfg.Chain)
	if err != nil {
		_ = policies.Close()
		_ = closeChains(resolver.chains)
		return nil, nil, fmt.Errorf("tun: %w", err)
	}
	planner, err := resolver.routePlanner(tunCfg.Chain, policies)
	if err != nil {
		_ = policies.Close()
		_ = closeChains(resolver.chains)
		return nil, nil, fmt.Errorf("tun rules: %w", err)
	}
	dnsProxy, err := dnsproxy.New(profile.DNS, planner)
	if err != nil {
		_ = policies.Close()
		_ = closeChains(resolver.chains)
		return nil, nil, fmt.Errorf("tun dns: %w", err)
	}
	opts := listener.TUNOptions{
		Name:          tunCfg.Name,
		ProfileName:   profile.Name,
		MTU:           tunCfg.MTU,
		Addresses:     tunCfg.Addresses,
		Routes:        tunCfg.Routes,
		ExcludeCIDRs:  tunCfg.ExcludeCIDRs,
		ChainName:     ch.Name,
		EventBus:      bus,
		DNSProxy:      dnsProxy,
		PolicyManager: policies,
	}
	return listener.NewPacketStack(opts, ch, planner, writer), resolver.chains, nil
}

type chainResolver struct {
	profile    *config.Profile
	configPath string
	tempRules  *temprules.Manager
	chains     []*chain.Chain
	byName     map[string]*chain.Chain
}

func newChainResolver(profile *config.Profile, configPath string, tempRules *temprules.Manager) *chainResolver {
	return &chainResolver{profile: profile, configPath: configPath, tempRules: tempRules}
}

// resolve picks the chain a listener should route through. An empty name
// selects the first chain in the profile. Each configured chain is converted
// at most once per active engine generation so listeners share dialer state.
func (r *chainResolver) resolve(name string) (*chain.Chain, error) {
	if len(r.profile.Chains) == 0 {
		return nil, errors.New("profile has no chains")
	}
	if err := r.ensureBuilt(); err != nil {
		return nil, err
	}
	if name == "" {
		return r.chains[0], nil
	}
	if ch, ok := r.byName[name]; ok {
		return ch, nil
	}
	return nil, fmt.Errorf("chain %q not found", name)
}

func (r *chainResolver) ensureBuilt() error {
	if r.chains != nil {
		return nil
	}
	r.chains = make([]*chain.Chain, 0, len(r.profile.Chains))
	r.byName = make(map[string]*chain.Chain, len(r.profile.Chains))
	for i := range r.profile.Chains {
		ch := chainFromConfig(r.profile.Chains[i])
		r.chains = append(r.chains, ch)
		if _, exists := r.byName[ch.Name]; !exists {
			r.byName[ch.Name] = ch
		}
	}
	return nil
}

func (r *chainResolver) routePlanner(defaultChainName string, policies *policy.Manager) (*routePlanner, error) {
	if err := r.ensureBuilt(); err != nil {
		return nil, err
	}
	if defaultChainName == "" {
		defaultChainName = r.chains[0].Name
	}
	known := make(map[string]struct{}, len(r.byName))
	for name := range r.byName {
		known[name] = struct{}{}
	}
	knownGroups := make(map[string]struct{}, len(r.profile.PolicyGroups))
	for _, group := range r.profile.PolicyGroups {
		knownGroups[group.Name] = struct{}{}
	}
	ruleSet := make([]rules.Rule, 0, len(r.profile.Rules))
	for _, rule := range r.profile.Rules {
		ruleSet = append(ruleSet, rules.Rule{
			Name:           rule.Name,
			Action:         rule.Action,
			RuleSets:       rule.RuleSets,
			Domains:        rule.Domains,
			DomainSuffixes: rule.DomainSuffixes,
			DomainKeywords: rule.DomainKeywords,
			CIDRs:          rule.CIDRs,
			SourceCIDRs:    rule.SourceCIDRs,
			Ports:          rule.Ports,
			Networks:       rule.Networks,
		})
	}
	ruleSets, _ := ruleset.Resolve(r.configPath, r.profile)
	engine, err := rules.CompileWithRuleSets(ruleSet, defaultChainName, known, knownGroups, ruleSets)
	if err != nil {
		return nil, err
	}
	return &routePlanner{profileName: r.profile.Name, rules: engine, chains: r.byName, policies: policies, defaultChainName: defaultChainName, tempRules: r.tempRules}, nil
}

type routePlanner struct {
	profileName      string
	rules            *rules.Engine
	chains           map[string]*chain.Chain
	policies         *policy.Manager
	defaultChainName string
	tempRules        *temprules.Manager
	dialer           net.Dialer
}

func (p *routePlanner) DefaultChainName() string {
	if p == nil {
		return ""
	}
	return p.defaultChainName
}

func (p *routePlanner) Plan(ctx context.Context, network, target string) (listener.RoutePlan, error) {
	return p.PlanWithSource(ctx, network, target, "")
}

func (p *routePlanner) PlanWithSource(ctx context.Context, network, target, source string) (listener.RoutePlan, error) {
	if p == nil || p.rules == nil {
		return listener.RoutePlan{}, errors.New("nil route planner")
	}
	decision, err := p.decide(network, target, source)
	if err != nil {
		return listener.RoutePlan{}, err
	}
	plan := listener.RoutePlan{
		Profile:   p.profileName,
		RuleName:  decision.RuleName,
		Action:    decision.Action,
		ChainName: decision.ChainName,
		GroupName: decision.GroupName,
		Target:    decision.Target,
		Host:      decision.Host,
		Port:      decision.Port,
		Network:   decision.Network,
		Source:    decision.Source,
		Default:   decision.Default,
		ElapsedNs: decision.ElapsedNs,
		Explanation: events.RouteExplanation{
			Source:        decision.Explanation.Source,
			RuleName:      decision.Explanation.RuleName,
			RuleNumber:    decision.Explanation.RuleNumber,
			MatcherKind:   decision.Explanation.MatcherKind,
			MatcherValue:  decision.Explanation.MatcherValue,
			DefaultChain:  decision.Explanation.DefaultChain,
			PolicyGroup:   decision.Explanation.PolicyGroup,
			SelectedChain: decision.Explanation.SelectedChain,
			FinalChain:    decision.Explanation.FinalChain,
			Summary:       decision.Explanation.Summary,
		},
	}
	plan.RouteControl = routeControlForDecision(decision, "", "")
	switch decision.Action {
	case rules.ActionChain:
		ch := p.chains[decision.ChainName]
		if ch == nil {
			return plan, fmt.Errorf("chain %q not found", decision.ChainName)
		}
		plan.Hops = ch.HopInfo()
		plan.Explanation.FinalChain = decision.ChainName
		plan.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			return ch.Dial(ctx, network, address)
		}
		plan.DialPacket = func(ctx context.Context, address string) (net.PacketConn, error) {
			return ch.DialPacket(ctx, address)
		}
	case rules.ActionGroup:
		if p.policies == nil {
			return plan, fmt.Errorf("policy group %q: manager is not configured", decision.GroupName)
		}
		ch, selected, reason, err := p.policies.SelectWithReason(decision.GroupName, policy.SelectionContext{
			Network: decision.Network,
			Target:  decision.Target,
			Source:  decision.Source,
		})
		if err != nil {
			return plan, err
		}
		plan.ChainName = selected
		plan.Explanation.SelectedChain = selected
		plan.Explanation.FinalChain = selected
		plan.RouteControl = routeControlForDecision(decision, selected, reason)
		plan.Hops = ch.HopInfo()
		plan.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			return ch.Dial(ctx, network, address)
		}
		plan.DialPacket = func(ctx context.Context, address string) (net.PacketConn, error) {
			return ch.DialPacket(ctx, address)
		}
	case rules.ActionDirect:
		plan.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			return p.dialer.DialContext(ctx, network, address)
		}
		plan.DialPacket = func(ctx context.Context, address string) (net.PacketConn, error) {
			return newDirectPacketConn(ctx, address)
		}
	case rules.ActionBlock:
	case rules.ActionReject:
	default:
		return plan, fmt.Errorf("unknown route action %q", decision.Action)
	}
	return plan, nil
}

func routeControlForDecision(decision rules.Decision, selectedChain, selectionReason string) events.RouteControl {
	source := strings.TrimSpace(decision.Explanation.Source)
	if source == "" {
		if decision.Default {
			source = "default"
		} else {
			source = "profile_rule"
		}
	}
	if selectedChain == "" {
		selectedChain = strings.TrimSpace(decision.ChainName)
	}
	policyGroup := strings.TrimSpace(decision.GroupName)
	if policyGroup == "" {
		policyGroup = strings.TrimSpace(decision.Explanation.PolicyGroup)
	}
	return events.RouteControl{
		Mode:            "rule",
		Decision:        routeControlDecision(decision.Action),
		Source:          source,
		RuleName:        decision.RuleName,
		RuleNumber:      decision.RuleNumber,
		PolicyGroup:     policyGroup,
		SelectedChain:   selectedChain,
		SelectionReason: selectionReason,
		Fallback:        selectionReason == "no_healthy_fallback",
		Default:         decision.Default,
	}
}

func routeControlDecision(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case rules.ActionDirect:
		return "direct"
	case rules.ActionBlock, rules.ActionReject:
		return "block"
	default:
		return "proxy"
	}
}

func (p *routePlanner) decide(network, target, source string) (rules.Decision, error) {
	knownChains := make(map[string]struct{}, len(p.chains))
	for name := range p.chains {
		knownChains[name] = struct{}{}
	}
	knownGroups := map[string]struct{}{}
	if p.policies != nil {
		for _, group := range p.policies.Snapshot(p.profileName).Groups {
			knownGroups[group.Name] = struct{}{}
		}
	}
	if p.tempRules != nil {
		decision, ok, err := p.tempRules.Decide(p.profileName, p.defaultChainName, network, target, source, knownChains, knownGroups)
		if err != nil {
			return rules.Decision{}, err
		}
		if ok {
			return decision, nil
		}
	}
	return p.rules.DecideWithSource(network, target, source), nil
}

type directPacketConn struct {
	*net.UDPConn
}

func newDirectPacketConn(ctx context.Context, _ string) (net.PacketConn, error) {
	var lc net.ListenConfig
	conn, err := lc.ListenPacket(ctx, "udp", "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	udp, ok := conn.(*net.UDPConn)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("direct UDP dial did not return UDPConn")
	}
	return &directPacketConn{UDPConn: udp}, nil
}

func (c *directPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		var err error
		udpAddr, err = net.ResolveUDPAddr("udp", addr.String())
		if err != nil {
			return 0, err
		}
	}
	return c.UDPConn.WriteToUDP(b, udpAddr)
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
