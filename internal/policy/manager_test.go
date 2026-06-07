package policy

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/protocol"
)

type testDialer struct {
}

func (d testDialer) Dial(context.Context, string, string) (protocol.Conn, error) {
	return nil, io.ErrClosedPipe
}

func (d testDialer) DialThrough(context.Context, io.ReadWriteCloser, string) (protocol.Conn, error) {
	return nil, io.ErrClosedPipe
}

func (d testDialer) Protocol() string {
	return "policy_test_tcp"
}

type testUDPDialer struct {
	testDialer
}

func (d testUDPDialer) Protocol() string {
	return "policy_test_udp"
}

func (d testUDPDialer) DialPacket(context.Context, string) (protocol.PacketConn, error) {
	return nil, io.ErrClosedPipe
}

func (d testUDPDialer) DialPacketThrough(context.Context, io.ReadWriteCloser, string) (protocol.PacketConn, error) {
	return nil, io.ErrClosedPipe
}

func init() {
	protocol.Register("policy_test_tcp", func(protocol.Server) (protocol.Dialer, error) {
		return testDialer{}, nil
	})
	protocol.Register("policy_test_udp", func(protocol.Server) (protocol.Dialer, error) {
		return testUDPDialer{}, nil
	})
}

func TestManagerSelectsLowestLatencyHealthyChain(t *testing.T) {
	m := newTestManager(t, []string{"slow", "fast"}, map[string]string{
		"slow": "policy_test_tcp",
		"fast": "policy_test_tcp",
	}, func(_ context.Context, ch *chain.Chain, _ string) ProbeResult {
		switch ch.Name {
		case "fast":
			return ProbeResult{Healthy: true, LatencyNs: int64(10 * time.Millisecond)}
		default:
			return ProbeResult{Healthy: true, LatencyNs: int64(50 * time.Millisecond)}
		}
	})

	if _, err := m.Refresh(context.Background(), "auto"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	_, selected, err := m.Select("auto", "tcp")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if selected != "fast" {
		t.Fatalf("selected = %q, want fast", selected)
	}
	snap := m.Snapshot("default")
	if snap.Profile != "default" || len(snap.Groups) != 1 || snap.Groups[0].SelectedChain != "fast" || len(snap.Groups[0].Results) != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestManagerFailsOpenToFirstChainWhenNoProbeIsHealthy(t *testing.T) {
	m := newTestManager(t, []string{"first", "second"}, map[string]string{
		"first":  "policy_test_tcp",
		"second": "policy_test_tcp",
	}, func(_ context.Context, ch *chain.Chain, _ string) ProbeResult {
		return ProbeResult{ChainName: ch.Name, Error: "timeout"}
	})

	if _, err := m.Refresh(context.Background(), "auto"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	_, selected, err := m.Select("auto", "tcp")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if selected != "first" {
		t.Fatalf("selected = %q, want first", selected)
	}
}

func TestManagerFiltersUDPIncapableChains(t *testing.T) {
	m := newTestManager(t, []string{"tcp", "udp"}, map[string]string{
		"tcp": "policy_test_tcp",
		"udp": "policy_test_udp",
	}, func(_ context.Context, ch *chain.Chain, _ string) ProbeResult {
		if ch.Name == "tcp" {
			return ProbeResult{Healthy: true, LatencyNs: int64(5 * time.Millisecond)}
		}
		return ProbeResult{Healthy: true, LatencyNs: int64(20 * time.Millisecond)}
	})

	if _, err := m.Refresh(context.Background(), "auto"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	_, selected, err := m.Select("auto", "tcp")
	if err != nil {
		t.Fatalf("Select tcp: %v", err)
	}
	if selected != "tcp" {
		t.Fatalf("tcp selected = %q, want tcp", selected)
	}
	_, selected, err = m.Select("auto", "udp")
	if err != nil {
		t.Fatalf("Select udp: %v", err)
	}
	if selected != "udp" {
		t.Fatalf("udp selected = %q, want udp", selected)
	}
}

func TestManagerReturnsUDPErrorWhenNoMemberSupportsUDP(t *testing.T) {
	m := newTestManager(t, []string{"first"}, map[string]string{
		"first": "policy_test_tcp",
	}, nil)

	if _, _, err := m.Select("auto", "udp"); err == nil {
		t.Fatal("Select udp error = nil, want capability error")
	}
}

func TestManagerSelectGroupUsesConfiguredSelection(t *testing.T) {
	chains := map[string]*chain.Chain{
		"tcp": {
			Name: "tcp",
			Nodes: []protocol.Server{{
				Name:     "tcp-server",
				Address:  "127.0.0.1:1",
				Protocol: "policy_test_tcp",
			}},
		},
		"udp": {
			Name: "udp",
			Nodes: []protocol.Server{{
				Name:     "udp-server",
				Address:  "127.0.0.1:1",
				Protocol: "policy_test_udp",
			}},
		},
	}
	t.Cleanup(func() {
		for _, ch := range chains {
			_ = ch.Close()
		}
	})
	m, err := New([]config.PolicyGroupConfig{{
		Name:     "manual",
		Type:     TypeSelect,
		Chains:   []string{"tcp", "udp"},
		Selected: "udp",
	}}, chains)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, selected, err := m.Select("manual", "tcp")
	if err != nil {
		t.Fatalf("Select tcp: %v", err)
	}
	if selected != "udp" {
		t.Fatalf("selected = %q, want udp", selected)
	}
	if err := m.SetSelection("manual", "tcp"); err != nil {
		t.Fatalf("SetSelection: %v", err)
	}
	_, selected, err = m.Select("manual", "tcp")
	if err != nil {
		t.Fatalf("Select after SetSelection: %v", err)
	}
	if selected != "tcp" {
		t.Fatalf("selected = %q, want tcp", selected)
	}
	if _, _, err := m.Select("manual", "udp"); err == nil {
		t.Fatal("Select udp error = nil, want selected chain capability error")
	}
}

func TestManagerStartRunsInitialProbe(t *testing.T) {
	probed := make(chan string, 1)
	m := newTestManager(t, []string{"first"}, map[string]string{
		"first": "policy_test_tcp",
	}, func(_ context.Context, ch *chain.Chain, _ string) ProbeResult {
		select {
		case probed <- ch.Name:
		default:
		}
		return ProbeResult{Healthy: true, LatencyNs: int64(time.Millisecond)}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	defer m.Close()

	select {
	case got := <-probed:
		if got != "first" {
			t.Fatalf("probed = %q, want first", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial probe")
	}
}

func newTestManager(t *testing.T, groupChains []string, protocols map[string]string, probe ProbeFunc) *Manager {
	t.Helper()
	chains := make(map[string]*chain.Chain, len(protocols))
	for name, proto := range protocols {
		chains[name] = &chain.Chain{
			Name: name,
			Nodes: []protocol.Server{{
				Name:     name + "-server",
				Address:  "127.0.0.1:1",
				Protocol: proto,
			}},
		}
		t.Cleanup(func() { _ = chains[name].Close() })
	}
	opts := []Option(nil)
	if probe != nil {
		opts = append(opts, WithProbeFunc(probe))
	}
	m, err := New([]config.PolicyGroupConfig{{
		Name:    "auto",
		Type:    TypeURLTest,
		Chains:  groupChains,
		TestURL: "https://probe.example/generate_204",
	}}, chains, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}
