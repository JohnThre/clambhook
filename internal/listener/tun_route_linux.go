//go:build linux

package listener

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"

	"github.com/JohnThre/clambhook/internal/chain"
)

type ipRunner interface {
	RunIP(ctx context.Context, args ...string) (string, error)
}

type execIPRunner struct{}

func (execIPRunner) RunIP(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "ip", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("ip %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

type trackedIPCommand struct {
	undo []string
}

type linuxRouteManager struct {
	ifName string
	mtu    int
	opts   TUNOptions
	ch     *chain.Chain
	runner ipRunner

	tracked []trackedIPCommand
}

func newLinuxRouteManager(ifName string, mtu int, opts TUNOptions, ch *chain.Chain) *linuxRouteManager {
	return &linuxRouteManager{
		ifName: ifName,
		mtu:    mtu,
		opts:   opts,
		ch:     ch,
		runner: execIPRunner{},
	}
}

func (m *linuxRouteManager) Setup(ctx context.Context) error {
	if m.runner == nil {
		m.runner = execIPRunner{}
	}

	exclusions, err := m.exclusionRoutes(ctx)
	if err != nil {
		return err
	}

	for _, addr := range tunAddresses(m.opts) {
		if _, err := netip.ParsePrefix(addr); err != nil {
			return fmt.Errorf("tun route: invalid address %q: %w", addr, err)
		}
		if err := m.runTracked(ctx,
			[]string{"addr", "add", addr, "dev", m.ifName},
			[]string{"addr", "del", addr, "dev", m.ifName},
		); err != nil {
			return err
		}
	}

	if _, err := m.runner.RunIP(ctx, "link", "set", "dev", m.ifName, "mtu", strconv.Itoa(m.mtu), "up"); err != nil {
		return err
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

	return nil
}

func (m *linuxRouteManager) Cleanup(ctx context.Context) error {
	var errs []error
	for i := len(m.tracked) - 1; i >= 0; i-- {
		if _, err := m.runner.RunIP(ctx, m.tracked[i].undo...); err != nil {
			errs = append(errs, err)
		}
	}
	m.tracked = nil
	_, _ = m.runner.RunIP(ctx, "link", "set", "dev", m.ifName, "down")
	return errors.Join(errs...)
}

func (m *linuxRouteManager) runTracked(ctx context.Context, do, undo []string) error {
	if _, err := m.runner.RunIP(ctx, do...); err != nil {
		return err
	}
	m.tracked = append(m.tracked, trackedIPCommand{undo: undo})
	return nil
}

type directRoute struct {
	prefix string
	info   routeInfo
}

type routeInfo struct {
	via string
	dev string
}

func (m *linuxRouteManager) exclusionRoutes(ctx context.Context) ([]directRoute, error) {
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
		out = append(out, directRoute{
			prefix: hostPrefix(ip),
			info:   info,
		})
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

func (m *linuxRouteManager) firstHopIPs(ctx context.Context) ([]netip.Addr, error) {
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

func (m *linuxRouteManager) routeInfoForIP(ctx context.Context, ip netip.Addr) (routeInfo, error) {
	out, err := m.runner.RunIP(ctx, familyFlag(ip), "route", "get", ip.String())
	if err != nil {
		return routeInfo{}, err
	}
	fields := strings.Fields(out)
	var info routeInfo
	for i := 0; i < len(fields)-1; i++ {
		switch fields[i] {
		case "via":
			info.via = fields[i+1]
		case "dev":
			info.dev = fields[i+1]
		}
	}
	if info.dev == "" {
		return routeInfo{}, fmt.Errorf("could not parse route dev from %q", strings.TrimSpace(out))
	}
	return info, nil
}

func (m *linuxRouteManager) tunRoutes(ctx context.Context) ([]string, error) {
	if len(m.opts.Routes) > 0 {
		for _, route := range m.opts.Routes {
			if _, err := netip.ParsePrefix(route); err != nil {
				return nil, fmt.Errorf("tun route: invalid route %q: %w", route, err)
			}
		}
		return append([]string(nil), m.opts.Routes...), nil
	}

	routes := []string{"0.0.0.0/1", "128.0.0.0/1"}
	hasV6, err := m.hasIPv6DefaultRoute(ctx)
	if err != nil {
		return nil, err
	}
	if hasV6 {
		routes = append(routes, "::/1", "8000::/1")
	}
	return routes, nil
}

func (m *linuxRouteManager) hasIPv6DefaultRoute(ctx context.Context) (bool, error) {
	out, err := m.runner.RunIP(ctx, "-6", "route", "show", "default")
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(out) != "", nil
}

func (m *linuxRouteManager) addTUNRoute(ctx context.Context, prefix string) error {
	family, err := familyFlagPrefix(prefix)
	if err != nil {
		return err
	}
	return m.runTracked(ctx,
		[]string{family, "route", "add", prefix, "dev", m.ifName},
		[]string{family, "route", "del", prefix, "dev", m.ifName},
	)
}

func (m *linuxRouteManager) addDirectRoute(ctx context.Context, prefix string, info routeInfo) error {
	family, err := familyFlagPrefix(prefix)
	if err != nil {
		return err
	}
	do := []string{family, "route", "add", prefix}
	undo := []string{family, "route", "del", prefix}
	if info.via != "" {
		do = append(do, "via", info.via)
		undo = append(undo, "via", info.via)
	}
	do = append(do, "dev", info.dev)
	undo = append(undo, "dev", info.dev)
	return m.runTracked(ctx, do, undo)
}

func familyFlag(ip netip.Addr) string {
	if ip.Is6() {
		return "-6"
	}
	return "-4"
}

func familyFlagPrefix(raw string) (string, error) {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return "", err
	}
	return familyFlag(prefix.Addr()), nil
}

func hostPrefix(ip netip.Addr) string {
	bits := 32
	if ip.Is6() {
		bits = 128
	}
	return netip.PrefixFrom(ip, bits).String()
}
