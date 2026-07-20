package listener

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

type planningContextObservation struct {
	hadDeadline bool
}

type dialContextObservation struct {
	hadDeadline bool
	remaining   time.Duration
}

type lifetimePlanner struct {
	planObserved chan planningContextObservation
	releasePlan  chan struct{}
	dialObserved chan dialContextObservation
	remote       chan net.Conn
}

func newLifetimePlanner() *lifetimePlanner {
	return &lifetimePlanner{
		planObserved: make(chan planningContextObservation, 1),
		releasePlan:  make(chan struct{}),
		dialObserved: make(chan dialContextObservation, 1),
		remote:       make(chan net.Conn, 1),
	}
}

func (p *lifetimePlanner) DefaultChainName() string { return "test" }

func (p *lifetimePlanner) Plan(ctx context.Context, network, target string) (RoutePlan, error) {
	_, hadDeadline := ctx.Deadline()
	p.planObserved <- planningContextObservation{hadDeadline: hadDeadline}
	select {
	case <-p.releasePlan:
	case <-ctx.Done():
		return RoutePlan{}, ctx.Err()
	}
	return RoutePlan{
		Action:    RouteActionChain,
		ChainName: "test",
		Network:   network,
		Target:    target,
		Dial: func(ctx context.Context, network, target string) (net.Conn, error) {
			deadline, hadDeadline := ctx.Deadline()
			remaining := time.Duration(0)
			if hadDeadline {
				remaining = time.Until(deadline)
			}
			p.dialObserved <- dialContextObservation{hadDeadline: hadDeadline, remaining: remaining}
			local, remote := net.Pipe()
			p.remote <- remote
			return local, nil
		},
	}, nil
}

func assertPlanningAndDialContexts(t *testing.T, p *lifetimePlanner) {
	t.Helper()
	select {
	case observation := <-p.planObserved:
		if observation.hadDeadline {
			t.Fatal("route planning inherited the 30s dial deadline; prompt waiting must use the handler lifetime")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("route planner was not called")
	}

	// Model time spent waiting on a prompt before allowing the route.
	time.Sleep(50 * time.Millisecond)
	close(p.releasePlan)

	select {
	case observation := <-p.dialObserved:
		if !observation.hadDeadline {
			t.Fatal("outbound dial has no timeout")
		}
		if observation.remaining < 29*time.Second {
			t.Fatalf("outbound dial budget = %v, want a fresh 30s budget after planning", observation.remaining)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("route dialer was not called")
	}
}

func TestSOCKSv5PlanningUsesHandlerLifetimeAndDialGetsFreshBudget(t *testing.T) {
	planner := newLifetimePlanner()
	s := NewSOCKSv5WithPlanner("127.0.0.1:0", nil, planner, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop() })

	client, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()
	if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(client, method); err != nil {
		t.Fatal(err)
	}
	req := append([]byte{0x05, cmdConnect, 0, atypDomain, 11}, []byte("example.com")...)
	req = append(req, 0, 80)
	if _, err := client.Write(req); err != nil {
		t.Fatal(err)
	}

	assertPlanningAndDialContexts(t, planner)
	remote := <-planner.remote
	defer remote.Close()
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read CONNECT reply: %v", err)
	}
	if reply[1] != repSuccess {
		t.Fatalf("CONNECT reply = %#x, want success", reply[1])
	}
}

func TestHTTPPlanningUsesHandlerLifetimeAndDialGetsFreshBudget(t *testing.T) {
	planner := newLifetimePlanner()
	s := NewHTTPWithPlanner("127.0.0.1:0", nil, planner, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop() })

	client, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()
	if _, err := fmt.Fprint(client, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatal(err)
	}

	assertPlanningAndDialContexts(t, planner)
	remote := <-planner.remote
	defer remote.Close()
	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", resp.StatusCode)
	}
}

func TestSOCKSv5StopDrainsEstablishedConnect(t *testing.T) {
	remoteCh := make(chan net.Conn, 1)
	s, addr := newTestListener(t, nil, stubDial(remoteCh))
	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(client, method); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte{0x05, cmdConnect, 0, atypIPv4, 1, 1, 1, 1, 0, 80}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatal(err)
	}
	remote := <-remoteCh
	defer remote.Close()

	stopped := make(chan error, 1)
	go func() { stopped <- s.Stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not drain established SOCKS5 CONNECT within one second")
	}
	if got := s.ActiveConns(); got != 0 {
		t.Fatalf("ActiveConns after Stop = %d, want 0", got)
	}
}

func TestSOCKSv5StopDrainsEstablishedUDPAssociate(t *testing.T) {
	fake := newFakePacketConn()
	s := &SOCKSv5{
		addr: "127.0.0.1:0",
		dial: unreachable(),
		dialPacket: func(context.Context, string) (net.PacketConn, error) {
			return fake, nil
		},
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	control, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if _, err := control.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(control, method); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Write([]byte{0x05, cmdUDPAssociate, 0, atypIPv4, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(control, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != repSuccess {
		t.Fatalf("UDP ASSOCIATE reply = %#x, want success", reply[1])
	}

	relayPort := binary.BigEndian.Uint16(reply[8:10])
	udpClient, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer udpClient.Close()
	datagram, err := encodeUDPDatagram("8.8.8.8", 53, []byte("query"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := udpClient.WriteToUDP(datagram, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(relayPort)}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fake.toChain:
	case <-time.After(2 * time.Second):
		t.Fatal("UDP session was not established")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- s.Stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not drain established SOCKS5 UDP association within one second")
	}
	if got := s.ActiveConns(); got != 0 {
		t.Fatalf("ActiveConns after Stop = %d, want 0", got)
	}
	fake.mu.Lock()
	closed := fake.closed
	fake.mu.Unlock()
	if !closed {
		t.Fatal("UDP chain session remained open after Stop")
	}
	_ = control.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := control.Read(make([]byte, 1)); err == nil {
		t.Fatal("UDP control connection remained open after Stop")
	}
}

func TestTUNPlanningUsesHandlerLifetimeAndDialGetsFreshBudget(t *testing.T) {
	planner := newLifetimePlanner()
	stack := NewPacketStack(TUNOptions{}, nil, planner, nil)
	type result struct {
		dialCtx context.Context
		cancel  context.CancelFunc
		err     error
	}
	done := make(chan result, 1)
	go func() {
		_, dialCtx, cancel, err := stack.planFlow(context.Background(), "tcp", "example.com:443", "10.0.0.2:12345")
		done <- result{dialCtx: dialCtx, cancel: cancel, err: err}
	}()

	select {
	case observation := <-planner.planObserved:
		if observation.hadDeadline {
			t.Fatal("TUN route planning inherited the 30s dial deadline; prompt waiting must use the handler lifetime")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TUN route planner was not called")
	}
	time.Sleep(50 * time.Millisecond)
	close(planner.releasePlan)

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("planFlow: %v", got.err)
		}
		defer got.cancel()
		deadline, ok := got.dialCtx.Deadline()
		if !ok {
			t.Fatal("TUN outbound dial has no timeout")
		}
		if remaining := time.Until(deadline); remaining < 29*time.Second {
			t.Fatalf("TUN outbound dial budget = %v, want a fresh 30s budget after planning", remaining)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TUN planning did not complete")
	}
}
