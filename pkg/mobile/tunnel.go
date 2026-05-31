package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/geo"
	"github.com/JohnThre/clambhook/internal/listener"
	"github.com/JohnThre/clambhook/internal/traffic"
)

const defaultTunnelMTU = 1500

var defaultTunnelAddresses = []string{
	"198.18.0.1/30",
	"fd7a:636c:616d::1/64",
}

var defaultTunnelRoutes = []string{
	"0.0.0.0/0",
	"::/0",
}

// PacketWriter is implemented by the native packet tunnel provider. The input
// is one raw IPv4 or IPv6 packet to write back to the system tunnel interface.
type PacketWriter interface {
	WritePacket(packet []byte) error
}

// TunnelRuntime runs clambhook's packet-stack runtime inside a mobile packet
// tunnel extension. It does not expose or bind the daemon HTTP API.
type TunnelRuntime struct {
	mu sync.Mutex

	writer PacketWriter
	cfg    *config.Config
	geo    *geo.Reader
	bus    *events.Bus
	trf    *traffic.Manager
	stack  *listener.PacketStack
	chains []*chain.Chain
	cancel context.CancelFunc
}

// NewTunnelRuntime creates an iOS/Android packet-tunnel runtime. iOS passes a
// NetworkExtension-backed writer; tests can provide any PacketWriter.
func NewTunnelRuntime(writer PacketWriter) *TunnelRuntime {
	return &TunnelRuntime{writer: writer}
}

// Start loads configPath and starts packet routing. If the active profile has
// no [listen.tun] stanza, mobile defaults are applied so existing daemon TOML
// can be used by the on-device tunnel.
func (r *TunnelRuntime) Start(configPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stack != nil {
		return nil
	}
	if r.writer == nil {
		return errors.New("tunnel: nil packet writer")
	}

	cfg, err := loadTunnelConfig(configPath)
	if err != nil {
		return err
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return err
	}

	bus := events.NewBus(events.DefaultConfig())
	geoReader, err := geo.Open(cfg.Geo.Database)
	if err != nil {
		log.Printf("geo: %v; continuing without geo lookups", err)
	}
	trafficMgr, err := traffic.NewManager(cfg.Traffic, func(address string) (*geo.Location, error) {
		return geoReader.Lookup(address)
	})
	if err != nil {
		if closeErr := geoReader.Close(); closeErr != nil {
			log.Printf("close geo after traffic start failure: %v", closeErr)
		}
		bus.Close()
		return fmt.Errorf("traffic: %w", err)
	}
	trafficMgr.Start(context.Background(), bus)

	profile, err := cfg.ActiveProfile()
	if err != nil {
		trafficMgr.Stop()
		_ = geoReader.Close()
		bus.Close()
		return err
	}
	stack, chains, err := engine.BuildPacketStack(profile, bus, r.writer)
	if err != nil {
		trafficMgr.Stop()
		_ = geoReader.Close()
		bus.Close()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := stack.Start(ctx); err != nil {
		cancel()
		closePacketChains(chains)
		trafficMgr.Stop()
		_ = geoReader.Close()
		bus.Close()
		return err
	}

	r.cfg = cfg
	r.geo = geoReader
	r.bus = bus
	r.trf = trafficMgr
	r.stack = stack
	r.chains = chains
	r.cancel = cancel
	log.Printf("clambhook mobile packet tunnel started")
	return nil
}

// Stop shuts down packet routing and releases chain, geo, traffic, and event
// resources.
func (r *TunnelRuntime) Stop() error {
	r.mu.Lock()
	cancel := r.cancel
	stack := r.stack
	chains := r.chains
	trf := r.trf
	geoReader := r.geo
	bus := r.bus
	r.cancel = nil
	r.stack = nil
	r.chains = nil
	r.trf = nil
	r.geo = nil
	r.bus = nil
	r.cfg = nil
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	var firstErr error
	if stack != nil {
		firstErr = stack.Stop()
	}
	if err := closePacketChains(chains); err != nil && firstErr == nil {
		firstErr = err
	}
	if trf != nil {
		trf.Stop()
	}
	if err := geoReader.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if bus != nil {
		bus.Close()
	}
	log.Printf("clambhook mobile packet tunnel stopped")
	return firstErr
}

// Reload restarts the packet stack with configPath when running, or just
// validates the config when idle.
func (r *TunnelRuntime) Reload(configPath string) error {
	r.mu.Lock()
	running := r.stack != nil
	r.mu.Unlock()
	if !running {
		cfg, err := loadTunnelConfig(configPath)
		if err != nil {
			return err
		}
		return engine.ValidateConfig(cfg)
	}
	if err := r.Stop(); err != nil {
		return err
	}
	return r.Start(configPath)
}

// SetActiveProfile switches the active profile and restarts the live packet
// stack against the new routing plan.
func (r *TunnelRuntime) SetActiveProfile(name string) error {
	r.mu.Lock()
	cfg := r.cfg
	r.mu.Unlock()
	if cfg == nil {
		return errors.New("tunnel: runtime is not running")
	}
	name = strings.TrimSpace(name)
	if _, ok := cfg.ProfileByName(name); !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	next := *cfg
	next.Active = name
	if err := engine.ValidateConfig(&next); err != nil {
		return err
	}
	return r.restartWithConfig(&next)
}

func (r *TunnelRuntime) restartWithConfig(cfg *config.Config) error {
	r.mu.Lock()
	r.cfg = cfg
	r.mu.Unlock()

	if err := r.Stop(); err != nil {
		return err
	}
	return r.startConfig(cfg)
}

func (r *TunnelRuntime) startConfig(cfg *config.Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stack != nil {
		return nil
	}
	if r.writer == nil {
		return errors.New("tunnel: nil packet writer")
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return err
	}

	bus := events.NewBus(events.DefaultConfig())
	geoReader, err := geo.Open(cfg.Geo.Database)
	if err != nil {
		log.Printf("geo: %v; continuing without geo lookups", err)
	}
	trafficMgr, err := traffic.NewManager(cfg.Traffic, func(address string) (*geo.Location, error) {
		return geoReader.Lookup(address)
	})
	if err != nil {
		_ = geoReader.Close()
		bus.Close()
		return fmt.Errorf("traffic: %w", err)
	}
	trafficMgr.Start(context.Background(), bus)
	profile, err := cfg.ActiveProfile()
	if err != nil {
		trafficMgr.Stop()
		_ = geoReader.Close()
		bus.Close()
		return err
	}
	stack, chains, err := engine.BuildPacketStack(profile, bus, r.writer)
	if err != nil {
		trafficMgr.Stop()
		_ = geoReader.Close()
		bus.Close()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := stack.Start(ctx); err != nil {
		cancel()
		closePacketChains(chains)
		trafficMgr.Stop()
		_ = geoReader.Close()
		bus.Close()
		return err
	}

	r.cfg = cfg
	r.geo = geoReader
	r.bus = bus
	r.trf = trafficMgr
	r.stack = stack
	r.chains = chains
	r.cancel = cancel
	return nil
}

// InjectPacket feeds one raw IP packet from the native tunnel interface into
// the Go userspace packet stack.
func (r *TunnelRuntime) InjectPacket(packet []byte) error {
	r.mu.Lock()
	stack := r.stack
	r.mu.Unlock()
	if stack == nil {
		return errors.New("tunnel: runtime is not running")
	}
	return stack.InjectPacket(packet)
}

func (r *TunnelRuntime) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stack != nil
}

func (r *TunnelRuntime) StatusJSON() (string, error) {
	r.mu.Lock()
	status := r.statusLocked()
	r.mu.Unlock()
	return marshalString(status)
}

func (r *TunnelRuntime) ProfilesJSON() (string, error) {
	r.mu.Lock()
	cfg := r.cfg
	r.mu.Unlock()
	if cfg == nil {
		return marshalString(profilesPayload{})
	}
	return marshalString(profilesPayload{Profiles: cfg.ProfileNames(), Active: cfg.Active})
}

func (r *TunnelRuntime) ServersJSON() (string, error) {
	r.mu.Lock()
	cfg := r.cfg
	geoReader := r.geo
	r.mu.Unlock()
	return marshalString(serversForConfig(cfg, geoReader))
}

func (r *TunnelRuntime) RulesJSON() (string, error) {
	r.mu.Lock()
	cfg := r.cfg
	r.mu.Unlock()
	return marshalString(rulesForConfig(cfg))
}

func (r *TunnelRuntime) TrafficJSON() (string, error) {
	r.mu.Lock()
	trf := r.trf
	r.mu.Unlock()
	if trf == nil || trf.Store() == nil {
		var empty *traffic.Store
		return marshalString(empty.Snapshot("all", 200))
	}
	return marshalString(trf.Store().Snapshot("all", 200))
}

func (r *TunnelRuntime) DashboardJSON() (string, error) {
	r.mu.Lock()
	cfg := r.cfg
	geoReader := r.geo
	trf := r.trf
	status := r.statusLocked()
	r.mu.Unlock()

	var trafficSnapshot traffic.Snapshot
	if trf == nil || trf.Store() == nil {
		var empty *traffic.Store
		trafficSnapshot = empty.Snapshot("all", 200)
	} else {
		trafficSnapshot = trf.Store().Snapshot("all", 200)
	}
	payload := dashboardPayload{
		Status: status,
		Profiles: profilesPayload{
			Profiles: profileNames(cfg),
			Active:   activeProfileName(cfg),
		},
		Servers: serversForConfig(cfg, geoReader),
		Rules:   rulesForConfig(cfg),
		Traffic: trafficSnapshot,
	}
	return marshalString(payload)
}

// TunnelNetworkSettingsJSON describes the NetworkExtension interface settings
// required for the active profile.
func TunnelNetworkSettingsJSON(configPath string) (string, error) {
	cfg, err := loadTunnelConfig(configPath)
	if err != nil {
		return "", err
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return "", err
	}
	profile, err := cfg.ActiveProfile()
	if err != nil {
		return "", err
	}
	return marshalString(networkSettingsForProfile(profile))
}

func (r *TunnelRuntime) statusLocked() statusPayload {
	status := statusPayload{Running: r.stack != nil, Profile: activeProfileName(r.cfg)}
	if r.stack != nil {
		status.Listeners = []listenerStatusPayload{{
			Protocol:    r.stack.Protocol(),
			Addr:        r.stack.Addr(),
			ActiveConns: r.stack.ActiveConns(),
		}}
	}
	return status
}

func loadTunnelConfig(configPath string) (*config.Config, error) {
	cfg, err := loadConfig(configPath, defaultAPIAddr)
	if err != nil {
		return nil, err
	}
	ensureTunnelConfig(cfg)
	return cfg, nil
}

func ensureTunnelConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	for i := range cfg.Profiles {
		tunCfg := cfg.Profiles[i].Listen.TUN
		if tunCfg == nil {
			tunCfg = &config.TUNConfig{Enabled: true}
			cfg.Profiles[i].Listen.TUN = tunCfg
		}
		tunCfg.Enabled = true
		if tunCfg.MTU == 0 {
			tunCfg.MTU = defaultTunnelMTU
		}
		if len(tunCfg.Routes) == 0 {
			tunCfg.Routes = append([]string(nil), defaultTunnelRoutes...)
		}
	}
}

func closePacketChains(chains []*chain.Chain) error {
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

func marshalString(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type statusPayload struct {
	Running   bool                    `json:"running"`
	Profile   string                  `json:"profile"`
	Listeners []listenerStatusPayload `json:"listeners,omitempty"`
}

type listenerStatusPayload struct {
	Protocol    string `json:"protocol"`
	Addr        string `json:"addr"`
	ActiveConns int64  `json:"active_conns"`
}

type profilesPayload struct {
	Profiles []string `json:"profiles"`
	Active   string   `json:"active"`
}

type serversPayload struct {
	Profile string         `json:"profile"`
	Chains  []chainPayload `json:"chains"`
}

type chainPayload struct {
	Name    string          `json:"name"`
	Servers []serverPayload `json:"servers"`
}

type serverPayload struct {
	Name     string        `json:"name"`
	Address  string        `json:"address"`
	Protocol string        `json:"protocol"`
	Geo      *geo.Location `json:"geo"`
	GeoError string        `json:"geo_error,omitempty"`
}

type rulesPayload struct {
	Profile string              `json:"profile"`
	Rules   []config.RuleConfig `json:"rules"`
}

type dashboardPayload struct {
	Status   statusPayload    `json:"status"`
	Profiles profilesPayload  `json:"profiles"`
	Servers  serversPayload   `json:"servers"`
	Rules    rulesPayload     `json:"rules"`
	Traffic  traffic.Snapshot `json:"traffic"`
}

type tunnelNetworkSettings struct {
	MTU            int               `json:"mtu"`
	RemoteAddress  string            `json:"remote_address"`
	IPv4           []ipPrefixSetting `json:"ipv4"`
	IPv6           []ipPrefixSetting `json:"ipv6"`
	IncludedRoutes []string          `json:"included_routes"`
	ExcludedRoutes []string          `json:"excluded_routes"`
}

type ipPrefixSetting struct {
	Address   string `json:"address"`
	PrefixLen int    `json:"prefix_len"`
}

func serversForConfig(cfg *config.Config, geoReader *geo.Reader) serversPayload {
	if cfg == nil {
		return serversPayload{}
	}
	profile, err := cfg.ActiveProfile()
	if err != nil {
		return serversPayload{}
	}
	payload := serversPayload{
		Profile: profile.Name,
		Chains:  make([]chainPayload, 0, len(profile.Chains)),
	}
	for _, ch := range profile.Chains {
		cp := chainPayload{Name: ch.Name, Servers: make([]serverPayload, 0, len(ch.Servers))}
		for _, server := range ch.Servers {
			loc, lookupErr := geoReader.Lookup(server.Address)
			row := serverPayload{
				Name:     server.Name,
				Address:  server.Address,
				Protocol: server.Protocol,
				Geo:      loc,
			}
			if lookupErr != nil {
				row.Geo = &geo.Location{}
				row.GeoError = lookupErr.Error()
			}
			cp.Servers = append(cp.Servers, row)
		}
		payload.Chains = append(payload.Chains, cp)
	}
	return payload
}

func rulesForConfig(cfg *config.Config) rulesPayload {
	if cfg == nil {
		return rulesPayload{}
	}
	profile, err := cfg.ActiveProfile()
	if err != nil {
		return rulesPayload{}
	}
	return rulesPayload{Profile: profile.Name, Rules: profile.Rules}
}

func networkSettingsForProfile(profile *config.Profile) tunnelNetworkSettings {
	tunCfg := profile.Listen.TUN
	settings := tunnelNetworkSettings{
		MTU:            defaultTunnelMTU,
		RemoteAddress:  firstServerHost(profile),
		IncludedRoutes: append([]string(nil), defaultTunnelRoutes...),
		ExcludedRoutes: nil,
	}
	if tunCfg != nil {
		if tunCfg.MTU > 0 {
			settings.MTU = tunCfg.MTU
		}
		if len(tunCfg.Routes) > 0 {
			settings.IncludedRoutes = append([]string(nil), tunCfg.Routes...)
		}
		settings.ExcludedRoutes = append([]string(nil), tunCfg.ExcludeCIDRs...)
	}
	addresses := defaultTunnelAddresses
	if tunCfg != nil && len(tunCfg.Addresses) > 0 {
		addresses = tunCfg.Addresses
	}
	for _, raw := range addresses {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			continue
		}
		row := ipPrefixSetting{Address: prefix.Addr().String(), PrefixLen: prefix.Bits()}
		if prefix.Addr().Is4() {
			settings.IPv4 = append(settings.IPv4, row)
		} else {
			settings.IPv6 = append(settings.IPv6, row)
		}
	}
	if settings.RemoteAddress == "" {
		settings.RemoteAddress = "127.0.0.1"
	}
	return settings
}

func firstServerHost(profile *config.Profile) string {
	if profile == nil {
		return ""
	}
	for _, ch := range profile.Chains {
		for _, server := range ch.Servers {
			host, _, err := net.SplitHostPort(server.Address)
			if err == nil {
				return strings.Trim(host, "[]")
			}
			if server.Address != "" {
				return strings.Trim(server.Address, "[]")
			}
		}
	}
	return ""
}

func profileNames(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	return cfg.ProfileNames()
}

func activeProfileName(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if cfg.Active != "" {
		return cfg.Active
	}
	if profile, err := cfg.ActiveProfile(); err == nil {
		return profile.Name
	}
	return ""
}
