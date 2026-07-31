//go:build linux

package listener

import (
	"context"
	"net/netip"
	"path/filepath"
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

func TestIPCommandIsAbsolute(t *testing.T) {
	// The privileged `ip` binary must be invoked by absolute path so an
	// attacker-influenced $PATH cannot redirect it to a planted binary.
	if !filepath.IsAbs(ipCommand) {
		t.Fatalf("ipCommand = %q, want an absolute path", ipCommand)
	}
}

type staticIPRunner struct{ out string }

func (s staticIPRunner) RunIP(context.Context, ...string) (string, error) {
	return s.out, nil
}

func TestRouteInfoForIPValidatesParsedFields(t *testing.T) {
	cases := []struct {
		name    string
		out     string
		wantErr bool
	}{
		{
			name: "valid via and dev",
			out:  "203.0.113.10 via 192.0.2.1 dev eth0 src 192.0.2.55\n",
		},
		{
			name:    "option-like dev rejected",
			out:     "203.0.113.10 via 192.0.2.1 dev -foo\n",
			wantErr: true,
		},
		{
			name:    "non-ip via rejected",
			out:     "203.0.113.10 via not-an-ip dev eth0\n",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newLinuxRouteManager("clambhook0", 1400, TUNOptions{}, nil)
			mgr.runner = staticIPRunner{out: tc.out}
			_, err := mgr.routeInfoForIP(context.Background(), netip.MustParseAddr("203.0.113.10"))
			if tc.wantErr && err == nil {
				t.Fatalf("routeInfoForIP(%q) = nil error, want rejection", tc.out)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("routeInfoForIP(%q) = %v, want nil", tc.out, err)
			}
		})
	}
}
