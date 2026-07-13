//go:build darwin

package listener

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JohnThre/clambhook/internal/chain"
)

const (
	darwinIfconfigCommand     = "/sbin/ifconfig"
	darwinRouteCommand        = "/sbin/route"
	darwinNetworksetupCommand = "/usr/sbin/networksetup"
	darwinDNSStatePath        = "/Library/Application Support/ClambHook/tun-state.json"
)

type darwinRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type execDarwinRunner struct{}

func (execDarwinRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

type trackedDarwinCommand struct {
	name string
	undo []string
}

type darwinRouteManager struct {
	ifName string
	mtu    int
	opts   TUNOptions
	ch     *chain.Chain
	runner darwinRunner

	tracked []trackedDarwinCommand
}

func newDarwinRouteManager(ifName string, mtu int, opts TUNOptions, ch *chain.Chain) *darwinRouteManager {
	return &darwinRouteManager{
		ifName: ifName,
		mtu:    mtu,
		opts:   opts,
		ch:     ch,
		runner: execDarwinRunner{},
	}
}

func (m *darwinRouteManager) Setup(ctx context.Context) error {
	if m.runner == nil {
		m.runner = execDarwinRunner{}
	}
	_ = m.restoreDNSState(ctx)

	exclusions, err := m.exclusionRoutes(ctx)
	if err != nil {
		return err
	}

	if err := m.runTracked(ctx,
		darwinIfconfigCommand,
		[]string{m.ifName, "mtu", strconv.Itoa(m.mtu), "up"},
		[]string{m.ifName, "down"},
	); err != nil {
		return err
	}

	for _, addr := range tunAddresses(m.opts) {
		prefix, err := netip.ParsePrefix(addr)
		if err != nil {
			return fmt.Errorf("tun route: invalid address %q: %w", addr, err)
		}
		if prefix.Addr().Is4() {
			if err := m.addIPv4Address(ctx, prefix); err != nil {
				return err
			}
		} else if err := m.addIPv6Address(ctx, prefix); err != nil {
			return err
		}
	}

	for _, route := range exclusions {
		if err := m.addDirectRoute(ctx, route.prefix, route.info); err != nil {
			return err
		}
	}

	routes, err := m.tunRoutes(ctx)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if err := m.addTUNRoute(ctx, route); err != nil {
			return err
		}
	}

	if err := m.configureDNS(ctx); err != nil {
		return err
	}
	return nil
}

func (m *darwinRouteManager) Cleanup(ctx context.Context) error {
	var errs []error
	if err := m.restoreDNSState(ctx); err != nil {
		errs = append(errs, err)
	}
	for i := len(m.tracked) - 1; i >= 0; i-- {
		if _, err := m.runner.Run(ctx, m.tracked[i].name, m.tracked[i].undo...); err != nil {
			errs = append(errs, err)
		}
	}
	m.tracked = nil
	_, _ = m.runner.Run(ctx, darwinIfconfigCommand, m.ifName, "down")
	return errors.Join(errs...)
}

func (m *darwinRouteManager) runTracked(ctx context.Context, name string, do, undo []string) error {
	if _, err := m.runner.Run(ctx, name, do...); err != nil {
		return err
	}
	m.tracked = append(m.tracked, trackedDarwinCommand{name: name, undo: undo})
	return nil
}

type directRoute struct {
	prefix string
	info   routeInfo
}

type routeInfo struct {
	gateway   string
	ifName    string
	linkLocal bool
}

func (m *darwinRouteManager) addIPv4Address(ctx context.Context, prefix netip.Prefix) error {
	mask := ipv4Mask(prefix.Bits())
	if mask == "" {
		return fmt.Errorf("tun route: invalid IPv4 prefix %s", prefix)
	}
	addr := prefix.Addr().String()
	return m.runTracked(ctx,
		darwinIfconfigCommand,
		[]string{m.ifName, "inet", addr, addr, "netmask", mask, "up"},
		[]string{m.ifName, "inet", addr, "delete"},
	)
}

func (m *darwinRouteManager) addIPv6Address(ctx context.Context, prefix netip.Prefix) error {
	addr := prefix.String()
	return m.runTracked(ctx,
		darwinIfconfigCommand,
		[]string{m.ifName, "inet6", addr, "up"},
		[]string{m.ifName, "inet6", prefix.Addr().String(), "delete"},
	)
}

func (m *darwinRouteManager) exclusionRoutes(ctx context.Context) ([]directRoute, error) {
	var out []directRoute
	firstHopIPs, err := m.firstHopIPs(ctx)
	if err != nil {
		return nil, err
	}
	for _, ip := range firstHopIPs {
		if ip.IsLoopback() {
			continue
		}
		info, err := m.routeInfoForIP(ctx, ip)
		if err != nil {
			return nil, fmt.Errorf("tun route: first hop %s: %w", ip, err)
		}
		out = append(out, directRoute{prefix: hostPrefix(ip), info: info})
	}

	for _, raw := range m.opts.ExcludeCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("tun route: invalid exclude_cidr %q: %w", raw, err)
		}
		if prefix.Bits() == 0 {
			return nil, fmt.Errorf("tun route: exclude_cidr %q would disable TUN routing", raw)
		}
		info, err := m.routeInfoForIP(ctx, prefix.Addr())
		if err != nil {
			return nil, fmt.Errorf("tun route: exclude_cidr %s: %w", raw, err)
		}
		out = append(out, directRoute{prefix: prefix.String(), info: info})
	}
	return out, nil
}

func (m *darwinRouteManager) firstHopIPs(ctx context.Context) ([]netip.Addr, error) {
	if m.ch == nil || len(m.ch.Nodes) == 0 {
		return nil, nil
	}
	raw := m.ch.Nodes[0].Address
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		host = raw
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return nil, nil
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("tun route: resolve first hop %q: %w", host, err)
	}
	seen := make(map[netip.Addr]struct{}, len(addrs))
	out := make([]netip.Addr, 0, len(addrs))
	for _, ip := range addrs {
		if _, ok := seen[ip]; ok {
			continue
		}
		seen[ip] = struct{}{}
		out = append(out, ip)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("tun route: resolve first hop %q: no addresses", host)
	}
	return out, nil
}

func (m *darwinRouteManager) routeInfoForIP(ctx context.Context, ip netip.Addr) (routeInfo, error) {
	out, err := m.runner.Run(ctx, darwinRouteCommand, "-n", "get", routeFamilyFlag(ip), ip.String())
	if err != nil {
		return routeInfo{}, err
	}
	var info routeInfo
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "gateway":
			info.gateway = value
		case "interface":
			info.ifName = value
		}
	}
	if info.gateway == "" && info.ifName == "" {
		return routeInfo{}, fmt.Errorf("could not parse route info from %q", strings.TrimSpace(out))
	}
	if info.gateway != "" {
		if _, err := netip.ParseAddr(strings.Trim(info.gateway, "[]")); err != nil {
			info.linkLocal = true
		}
	}
	return info, nil
}

func (m *darwinRouteManager) tunRoutes(ctx context.Context) ([]string, error) {
	if len(m.opts.Routes) > 0 {
		for _, route := range m.opts.Routes {
			if _, err := netip.ParsePrefix(route); err != nil {
				return nil, fmt.Errorf("tun route: invalid route %q: %w", route, err)
			}
		}
		return append([]string(nil), m.opts.Routes...), nil
	}

	routes := []string{"0.0.0.0/1", "128.0.0.0/1"}
	if m.hasIPv6DefaultRoute(ctx) {
		routes = append(routes, "::/1", "8000::/1")
	}
	return routes, nil
}

func (m *darwinRouteManager) hasIPv6DefaultRoute(ctx context.Context) bool {
	out, err := m.runner.Run(ctx, darwinRouteCommand, "-n", "get", "-inet6", "default")
	return err == nil && strings.TrimSpace(out) != ""
}

func (m *darwinRouteManager) addTUNRoute(ctx context.Context, prefix string) error {
	args, err := routePrefixArgs("add", prefix)
	if err != nil {
		return err
	}
	args = append(args, "-interface", m.ifName)
	undo, err := routePrefixArgs("delete", prefix)
	if err != nil {
		return err
	}
	undo = append(undo, "-interface", m.ifName)
	return m.runTracked(ctx, darwinRouteCommand, args, undo)
}

func (m *darwinRouteManager) addDirectRoute(ctx context.Context, prefix string, info routeInfo) error {
	args, err := routePrefixArgs("add", prefix)
	if err != nil {
		return err
	}
	undo, err := routePrefixArgs("delete", prefix)
	if err != nil {
		return err
	}
	if info.gateway != "" && !info.linkLocal {
		args = append(args, info.gateway)
		undo = append(undo, info.gateway)
	} else if info.ifName != "" {
		args = append(args, "-interface", info.ifName)
		undo = append(undo, "-interface", info.ifName)
	} else {
		return fmt.Errorf("tun route: route has no gateway or interface")
	}
	return m.runTracked(ctx, darwinRouteCommand, args, undo)
}

func routePrefixArgs(action, raw string) ([]string, error) {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return nil, err
	}
	args := []string{"-n", action, prefixFamilyFlag(prefix)}
	if prefix.Bits() == addressBits(prefix.Addr()) {
		args = append(args, "-host", prefix.Addr().String())
	} else {
		args = append(args, "-net", prefix.String())
	}
	return args, nil
}

func routeFamilyFlag(ip netip.Addr) string {
	if ip.Is6() {
		return "-inet6"
	}
	return "-inet"
}

func prefixFamilyFlag(prefix netip.Prefix) string {
	return routeFamilyFlag(prefix.Addr())
}

func addressBits(ip netip.Addr) int {
	if ip.Is6() {
		return 128
	}
	return 32
}

func hostPrefix(ip netip.Addr) string {
	return netip.PrefixFrom(ip, addressBits(ip)).String()
}

func ipv4Mask(bits int) string {
	if bits < 0 || bits > 32 {
		return ""
	}
	mask := net.CIDRMask(bits, 32)
	return fmt.Sprintf("%d.%d.%d.%d", mask[0], mask[1], mask[2], mask[3])
}

func (m *darwinRouteManager) configureDNS(ctx context.Context) error {
	if m.opts.DNSProxy == nil {
		return nil
	}
	dnsAddr := tunDNSAddress(m.opts)
	if dnsAddr == "" {
		return nil
	}
	services, err := m.networkServices(ctx)
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return nil
	}
	state := darwinDNSState{Services: make([]darwinDNSServiceState, 0, len(services))}
	for _, service := range services {
		servers, automatic, err := m.currentDNSServers(ctx, service)
		if err != nil {
			return err
		}
		state.Services = append(state.Services, darwinDNSServiceState{
			Name:      service,
			Servers:   servers,
			Automatic: automatic,
		})
	}
	if err := writeDarwinDNSState(state); err != nil {
		return err
	}
	for _, service := range services {
		if err := m.setDNSServers(ctx, service, []string{dnsAddr}); err != nil {
			return err
		}
	}
	return nil
}

func (m *darwinRouteManager) restoreDNSState(ctx context.Context) error {
	state, err := readDarwinDNSState()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var errs []error
	for _, service := range state.Services {
		servers := service.Servers
		if service.Automatic || len(servers) == 0 {
			servers = nil
		}
		if err := m.setDNSServers(ctx, service.Name, servers); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		if err := os.Remove(darwinDNSStatePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *darwinRouteManager) networkServices(ctx context.Context) ([]string, error) {
	out, err := m.runner.Run(ctx, darwinNetworksetupCommand, "-listallnetworkservices")
	if err != nil {
		return nil, err
	}
	var services []string
	for _, line := range strings.Split(out, "\n") {
		service := strings.TrimSpace(line)
		if service == "" || strings.HasPrefix(service, "*") || strings.HasPrefix(service, "An asterisk") {
			continue
		}
		services = append(services, service)
	}
	return services, nil
}

func (m *darwinRouteManager) currentDNSServers(ctx context.Context, service string) ([]string, bool, error) {
	out, err := m.runner.Run(ctx, darwinNetworksetupCommand, "-getdnsservers", service)
	if err != nil {
		return nil, false, err
	}
	var servers []string
	automatic := false
	for _, line := range strings.Split(out, "\n") {
		value := strings.TrimSpace(line)
		if value == "" {
			continue
		}
		if strings.HasPrefix(value, "There aren't any DNS Servers set") {
			automatic = true
			continue
		}
		servers = append(servers, value)
	}
	return servers, automatic || len(servers) == 0, nil
}

func (m *darwinRouteManager) setDNSServers(ctx context.Context, service string, servers []string) error {
	args := []string{"-setdnsservers", service}
	if len(servers) == 0 {
		args = append(args, "Empty")
	} else {
		args = append(args, servers...)
	}
	_, err := m.runner.Run(ctx, darwinNetworksetupCommand, args...)
	return err
}

type darwinDNSState struct {
	Services []darwinDNSServiceState `json:"services"`
}

type darwinDNSServiceState struct {
	Name      string   `json:"name"`
	Servers   []string `json:"servers,omitempty"`
	Automatic bool     `json:"automatic,omitempty"`
}

func readDarwinDNSState() (darwinDNSState, error) {
	data, err := os.ReadFile(darwinDNSStatePath)
	if err != nil {
		return darwinDNSState{}, err
	}
	var state darwinDNSState
	if err := json.Unmarshal(data, &state); err != nil {
		return darwinDNSState{}, err
	}
	return state, nil
}

func writeDarwinDNSState(state darwinDNSState) error {
	if err := os.MkdirAll(filepath.Dir(darwinDNSStatePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(darwinDNSStatePath, append(data, '\n'), 0o600)
}

func tunDNSAddress(opts TUNOptions) string {
	var first string
	for _, raw := range tunAddresses(opts) {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			continue
		}
		addr := prefix.Addr()
		if first == "" {
			first = addr.String()
		}
		if addr.Is4() {
			return addr.String()
		}
	}
	return first
}
