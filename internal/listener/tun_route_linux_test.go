//go:build linux

package listener

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/protocol"
)

type fakeIPRunner struct {
	commands []string
}

func (f *fakeIPRunner) RunIP(_ context.Context, args ...string) (string, error) {
	cmd := strings.Join(args, " ")
	f.commands = append(f.commands, cmd)
	switch cmd {
	case "-4 route get 203.0.113.10":
		return "203.0.113.10 via 192.0.2.1 dev eth0 src 192.0.2.55 uid 1000\n", nil
	case "-4 route get 10.0.0.0":
		return "10.0.0.0 via 192.0.2.1 dev eth0 src 192.0.2.55 uid 1000\n", nil
	case "-6 route show default":
		return "", nil
	default:
		return "", nil
	}
}

func TestLinuxRouteManagerSetupAndCleanupCommands(t *testing.T) {
	runner := &fakeIPRunner{}
	ch := &chain.Chain{
		Name: "main",
		Nodes: []protocol.Server{{
			Name:     "exit",
			Address:  "203.0.113.10:443",
			Protocol: "trojan",
			Settings: map[string]any{"password": "secret"},
		}},
	}
	mgr := newLinuxRouteManager("clambhook-test0", 1400, TUNOptions{
		Addresses:    []string{"198.18.0.1/30"},
		ExcludeCIDRs: []string{"10.0.0.0/8"},
	}, ch)
	mgr.runner = runner

	if err := mgr.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := mgr.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	want := []string{
		"-4 route get 203.0.113.10",
		"-4 route get 10.0.0.0",
		"addr add 198.18.0.1/30 dev clambhook-test0",
		"link set dev clambhook-test0 mtu 1400 up",
		"-4 route add 203.0.113.10/32 via 192.0.2.1 dev eth0",
		"-4 route add 10.0.0.0/8 via 192.0.2.1 dev eth0",
		"-6 route show default",
		"-4 route add 0.0.0.0/1 dev clambhook-test0",
		"-4 route add 128.0.0.0/1 dev clambhook-test0",
		"-4 route del 128.0.0.0/1 dev clambhook-test0",
		"-4 route del 0.0.0.0/1 dev clambhook-test0",
		"-4 route del 10.0.0.0/8 via 192.0.2.1 dev eth0",
		"-4 route del 203.0.113.10/32 via 192.0.2.1 dev eth0",
		"addr del 198.18.0.1/30 dev clambhook-test0",
		"link set dev clambhook-test0 down",
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands mismatch\n got: %#v\nwant: %#v", runner.commands, want)
	}
}
