//go:build darwin

package listener

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/protocol"
)

// fakeDarwinRunner records every command it is asked to run and replays
// canned output/errors keyed on the full "name args..." string. Commands with
// no registered response return an empty string and nil error, mirroring the
// idempotent ifconfig/route mutators the manager does not read output from.
type fakeDarwinRunner struct {
	commands  []string
	responses map[string]string
	errs      map[string]error
}

func (f *fakeDarwinRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	cmd := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.commands = append(f.commands, cmd)
	return f.responses[cmd], f.errs[cmd]
}

const (
	testRouteGet203  = "/sbin/route -n get -inet 203.0.113.10"
	testRouteGet10   = "/sbin/route -n get -inet 10.0.0.0"
	testRouteV6Def   = "/sbin/route -n get -inet6 default"
	testListServices = "/usr/sbin/networksetup -listallnetworkservices"
	testGetDNSWiFi   = "/usr/sbin/networksetup -getdnsservers Wi-Fi"
	testGetDNSEth    = "/usr/sbin/networksetup -getdnsservers Ethernet"
)

func routeInfoOutput() string {
	return "   route to: 203.0.113.10\n    gateway: 192.0.2.1\n  interface: en0\n"
}

func baseDarwinResponses() map[string]string {
	return map[string]string{
		testRouteGet203:  routeInfoOutput(),
		testRouteGet10:   routeInfoOutput(),
		testListServices: "An asterisk (*) denotes that a network service is disabled.\nWi-Fi\nEthernet\n",
		testGetDNSWiFi:   "8.8.8.8\n8.8.4.4\n",
		testGetDNSEth:    "There aren't any DNS Servers set on Ethernet.\n",
	}
}

func newTestChain() *chain.Chain {
	return &chain.Chain{
		Name: "main",
		Nodes: []protocol.Server{{
			Name:     "exit",
			Address:  "203.0.113.10:443",
			Protocol: "trojan",
			Settings: map[string]any{"password": "secret"},
		}},
	}
}

// stubDNSProxy satisfies the DNSProxy interface so configureDNS engages.
type stubDNSProxy struct{}

func (stubDNSProxy) Exchange(context.Context, []byte) ([]byte, error) { return nil, nil }
func (stubDNSProxy) Close() error                                     { return nil }

// TestDarwinRouteManagerSetupAndCleanupCommands pins the exact command
// sequence for a full Enhanced Mode bring-up: first-hop + exclude-CIDR direct
// routes are installed before the split-default TUN routes, DNS is rewritten,
// and Cleanup unwinds every tracked mutation in reverse order (rollback
// ordering) after restoring DNS.
func TestDarwinRouteManagerSetupAndCleanupCommands(t *testing.T) {
	runner := &fakeDarwinRunner{responses: baseDarwinResponses()}
	mgr := newDarwinRouteManager("utunTest", 1400, TUNOptions{
		Addresses:    []string{"198.18.0.1/30"},
		ExcludeCIDRs: []string{"10.0.0.0/8"},
		DNSProxy:     stubDNSProxy{},
	}, newTestChain())
	mgr.runner = runner
	mgr.dnsStatePath = filepath.Join(t.TempDir(), "tun-state.json")

	if err := mgr.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := mgr.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	want := []string{
		// exclusion route discovery
		testRouteGet203,
		testRouteGet10,
		// interface up + address
		"/sbin/ifconfig utunTest mtu 1400 up",
		"/sbin/ifconfig utunTest inet 198.18.0.1 198.18.0.1 netmask 255.255.255.252 up",
		// direct routes preserve first hop + excluded CIDR
		"/sbin/route -n add -inet -host 203.0.113.10 192.0.2.1",
		"/sbin/route -n add -inet -net 10.0.0.0/8 192.0.2.1",
		// split-default TUN routes (no IPv6 default present)
		testRouteV6Def,
		"/sbin/route -n add -inet -net 0.0.0.0/1 -interface utunTest",
		"/sbin/route -n add -inet -net 128.0.0.0/1 -interface utunTest",
		// DNS rewrite
		testListServices,
		testGetDNSWiFi,
		testGetDNSEth,
		"/usr/sbin/networksetup -setdnsservers Wi-Fi 198.18.0.1",
		"/usr/sbin/networksetup -setdnsservers Ethernet 198.18.0.1",
		// Cleanup: DNS restored first (Wi-Fi originals, Ethernet back to automatic)
		"/usr/sbin/networksetup -setdnsservers Wi-Fi 8.8.8.8 8.8.4.4",
		"/usr/sbin/networksetup -setdnsservers Ethernet Empty",
		// tracked mutations undone in reverse order
		"/sbin/route -n delete -inet -net 128.0.0.0/1 -interface utunTest",
		"/sbin/route -n delete -inet -net 0.0.0.0/1 -interface utunTest",
		"/sbin/route -n delete -inet -net 10.0.0.0/8 192.0.2.1",
		"/sbin/route -n delete -inet -host 203.0.113.10 192.0.2.1",
		"/sbin/ifconfig utunTest inet 198.18.0.1 delete",
		"/sbin/ifconfig utunTest down",
		// final safety down
		"/sbin/ifconfig utunTest down",
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands mismatch\n got: %#v\nwant: %#v", runner.commands, want)
	}

	if _, err := os.Stat(mgr.dnsStatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dns state file should be removed after restore, stat err = %v", err)
	}
}

// TestDarwinRouteManagerAddsIPv6SplitRoutesWhenDefaultPresent verifies that
// IPv6 split-default routes are installed only when the host already has an
// IPv6 default route.
func TestDarwinRouteManagerAddsIPv6SplitRoutesWhenDefaultPresent(t *testing.T) {
	responses := baseDarwinResponses()
	responses[testRouteV6Def] = "   route to: default\n    gateway: fe80::1\n  interface: en0\n"

	runner := &fakeDarwinRunner{responses: responses}
	mgr := newDarwinRouteManager("utunTest", 1400, TUNOptions{
		Addresses: []string{"198.18.0.1/30"},
	}, newTestChain())
	mgr.runner = runner
	mgr.dnsStatePath = filepath.Join(t.TempDir(), "tun-state.json")

	if err := mgr.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	wantRoutes := []string{
		"/sbin/route -n add -inet -net 0.0.0.0/1 -interface utunTest",
		"/sbin/route -n add -inet -net 128.0.0.0/1 -interface utunTest",
		"/sbin/route -n add -inet6 -net ::/1 -interface utunTest",
		"/sbin/route -n add -inet6 -net 8000::/1 -interface utunTest",
	}
	for _, want := range wantRoutes {
		if !containsCommand(runner.commands, want) {
			t.Fatalf("missing route command %q in %#v", want, runner.commands)
		}
	}
}

// TestDarwinRouteManagerNoIPv6SplitRoutesWithoutDefault verifies that an
// IPv4-only host (no IPv6 default route) does not get IPv6 TUN routes.
func TestDarwinRouteManagerNoIPv6SplitRoutesWithoutDefault(t *testing.T) {
	runner := &fakeDarwinRunner{responses: baseDarwinResponses()}
	mgr := newDarwinRouteManager("utunTest", 1400, TUNOptions{
		Addresses: []string{"198.18.0.1/30"},
	}, newTestChain())
	mgr.runner = runner
	mgr.dnsStatePath = filepath.Join(t.TempDir(), "tun-state.json")

	if err := mgr.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	for _, cmd := range runner.commands {
		if strings.Contains(cmd, "-inet6") && strings.Contains(cmd, "add") {
			t.Fatalf("unexpected IPv6 route add without default: %q", cmd)
		}
	}
}

// TestDarwinRouteManagerCustomRoutesBypassSplitDefault verifies that an
// explicit route list is used verbatim and the split-default heuristic
// (including the IPv6 default probe) is skipped.
func TestDarwinRouteManagerCustomRoutesBypassSplitDefault(t *testing.T) {
	runner := &fakeDarwinRunner{responses: baseDarwinResponses()}
	mgr := newDarwinRouteManager("utunTest", 1400, TUNOptions{
		Addresses: []string{"198.18.0.1/30"},
		Routes:    []string{"172.16.0.0/12"},
	}, newTestChain())
	mgr.runner = runner
	mgr.dnsStatePath = filepath.Join(t.TempDir(), "tun-state.json")

	if err := mgr.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if !containsCommand(runner.commands, "/sbin/route -n add -inet -net 172.16.0.0/12 -interface utunTest") {
		t.Fatalf("custom route not installed: %#v", runner.commands)
	}
	for _, cmd := range runner.commands {
		if cmd == testRouteV6Def {
			t.Fatalf("IPv6 default probe should be skipped for custom routes")
		}
		if strings.Contains(cmd, "0.0.0.0/1") {
			t.Fatalf("split-default route installed despite custom routes: %q", cmd)
		}
	}
}

// TestDarwinDNSRestoreRecoversStaleStateOnSetup proves the crash-recovery
// path: a state file left behind by an ungraceful stop is restored at the very
// start of the next Setup, before DNS is reconfigured.
func TestDarwinDNSRestoreRecoversStaleStateOnSetup(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "tun-state.json")
	stale := darwinDNSState{Services: []darwinDNSServiceState{
		{Name: "Wi-Fi", Servers: []string{"1.1.1.1"}},
	}}

	runner := &fakeDarwinRunner{responses: baseDarwinResponses()}
	mgr := newDarwinRouteManager("utunTest", 1400, TUNOptions{
		Addresses: []string{"198.18.0.1/30"},
		DNSProxy:  stubDNSProxy{},
	}, newTestChain())
	mgr.runner = runner
	mgr.dnsStatePath = statePath
	if err := mgr.writeDNSState(stale); err != nil {
		t.Fatalf("seed stale state: %v", err)
	}

	if err := mgr.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	recover := indexOfCommand(runner.commands, "/usr/sbin/networksetup -setdnsservers Wi-Fi 1.1.1.1")
	if recover < 0 {
		t.Fatalf("stale DNS not recovered on setup: %#v", runner.commands)
	}
	rewrite := indexOfCommand(runner.commands, "/usr/sbin/networksetup -setdnsservers Wi-Fi 198.18.0.1")
	if rewrite < 0 {
		t.Fatalf("tun DNS not applied after recovery: %#v", runner.commands)
	}
	if recover > rewrite {
		t.Fatalf("crash recovery (%d) must precede DNS rewrite (%d)", recover, rewrite)
	}
}

// TestDarwinCleanupAggregatesUndoErrors verifies that Cleanup keeps unwinding
// after a failing undo command, joins every error, and still forces the
// interface down.
func TestDarwinCleanupAggregatesUndoErrors(t *testing.T) {
	runner := &fakeDarwinRunner{
		responses: baseDarwinResponses(),
		errs:      map[string]error{},
	}
	mgr := newDarwinRouteManager("utunTest", 1400, TUNOptions{
		Addresses: []string{"198.18.0.1/30"},
	}, newTestChain())
	mgr.runner = runner
	mgr.dnsStatePath = filepath.Join(t.TempDir(), "tun-state.json")

	if err := mgr.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Fail one route deletion during cleanup.
	failCmd := "/sbin/route -n delete -inet -net 0.0.0.0/1 -interface utunTest"
	runner.errs[failCmd] = errors.New("route delete boom")
	runner.commands = nil

	err := mgr.Cleanup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "route delete boom") {
		t.Fatalf("Cleanup error = %v, want aggregated undo failure", err)
	}
	// Later undos still ran despite the earlier failure.
	if !containsCommand(runner.commands, "/sbin/ifconfig utunTest inet 198.18.0.1 delete") {
		t.Fatalf("address undo skipped after failing route delete: %#v", runner.commands)
	}
	if runner.commands[len(runner.commands)-1] != "/sbin/ifconfig utunTest down" {
		t.Fatalf("interface not forced down last: %#v", runner.commands)
	}
}

func containsCommand(commands []string, want string) bool {
	return indexOfCommand(commands, want) >= 0
}

func indexOfCommand(commands []string, want string) int {
	for i, cmd := range commands {
		if cmd == want {
			return i
		}
	}
	return -1
}
