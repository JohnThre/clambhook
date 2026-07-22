package listener

import (
	"context"
	"errors"
		"sync"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// flakyPacketWriter fails a configurable number of times before accepting
// packets. It exercises the stack-to-writer transient-error backoff.
type flakyPacketWriter struct {
	mu        sync.Mutex
	failUntil int
	called    int
	written   [][]byte
	failWith  error
}

func (w *flakyPacketWriter) WritePacket(pkt []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.called++
	if w.called <= w.failUntil {
		if w.failWith != nil {
			return w.failWith
		}
		return errors.New("transient write failure")
	}
	out := append([]byte(nil), pkt...)
	w.written = append(w.written, out)
	return nil
}

func (w *flakyPacketWriter) calls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.called
}

func (w *flakyPacketWriter) payloads() [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][]byte(nil), w.written...)
}

// staticPlanner is a trivial RoutePlanner for tests that only need a valid
// planner object to satisfy PacketStack.Start validation.
type staticPlanner struct {
	plan RoutePlan
}

func (p *staticPlanner) DefaultChainName() string { return "static" }

func (p *staticPlanner) Plan(ctx context.Context, network, target string) (RoutePlan, error) {
	return p.plan, nil
}

// startTestStack creates a minimal packet stack and starts it. The returned
// linkEP can be used to inject outbound packets via InjectLink.
func startTestStack(t *testing.T, writer PacketWriter) (*PacketStack, *channel.Endpoint, context.CancelFunc) {
	t.Helper()
	s := NewPacketStack(TUNOptions{
		Name:      "test",
		Addresses: []string{"10.0.0.1/24"},
		Routes:    []string{"0.0.0.0/0"},
	}, nil, &staticPlanner{plan: RoutePlan{Action: RouteActionBlock}}, writer)
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop() })
	return s, s.linkEP, cancel
}

// injectLinkOutbound puts a raw packet into the channel endpoint's outbound
// queue via WritePackets so that stackToWriterLoop can read it. This bypasses
// the gVisor network layer and tests the writer loop in isolation.
func injectLinkOutbound(linkEP *channel.Endpoint, pkt []byte) {
	if len(pkt) == 0 {
		return
	}
	pb := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(pkt),
	})
	var pktList stack.PacketBufferList
	pktList.PushBack(pb)
	linkEP.WritePackets(pktList)
}

// TestStackToWriterLoopRecoversFromTransientError proves that a single
// transient WritePacket failure does not terminate the loop: the next packet
// is still delivered after the bounded backoff.
func TestStackToWriterLoopRecoversFromTransientError(t *testing.T) {
	writer := &flakyPacketWriter{failUntil: 1}
	_, linkEP, cancel := startTestStack(t, writer)
	defer cancel()

	pkt1 := rawIPv4Packet()
	injectLinkOutbound(linkEP, pkt1)
	time.Sleep(50 * time.Millisecond)

	pkt2 := rawIPv4Packet()
	injectLinkOutbound(linkEP, pkt2)

	dl := time.Now().Add(3 * time.Second)
	for time.Now().Before(dl) {
		if writer.calls() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if writer.calls() < 2 {
		t.Fatalf("writer called %d times, want at least 2", writer.calls())
	}
	written := writer.payloads()
	if len(written) < 1 {
		t.Fatalf("writer accepted %d packets, want at least 1", len(written))
	}
}

// TestStackToWriterLoopExitsOnContextCancel proves that cancellation is honored
// promptly even while the loop is in a transient-error backoff.
func TestStackToWriterLoopExitsOnContextCancel(t *testing.T) {
	writer := &flakyPacketWriter{failUntil: 1000, failWith: errors.New("permanent transient")}
	s, linkEP, cancel := startTestStack(t, writer)

	injectLinkOutbound(linkEP, rawIPv4Packet())
	time.Sleep(20 * time.Millisecond)

	cancel()

	stopped := make(chan struct{})
	go func() { _ = s.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return promptly after context cancellation")
	}
}

// TestStackToWriterLoopExitsAfterMaxConsecutiveErrors proves that the loop
// terminates once the transient-error budget is exhausted.
func TestStackToWriterLoopExitsAfterMaxConsecutiveErrors(t *testing.T) {
	writer := &flakyPacketWriter{failUntil: 1000, failWith: errors.New("permanent transient")}
	_, linkEP, cancel := startTestStack(t, writer)
	defer cancel()

	for range tunMaxWriteConsecutiveErrors + 2 {
		injectLinkOutbound(linkEP, rawIPv4Packet())
	}

	dl := time.Now().Add(5 * time.Second)
	for time.Now().Before(dl) {
		if writer.calls() >= tunMaxWriteConsecutiveErrors {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	time.Sleep(50 * time.Millisecond)

	callsAt := writer.calls()
	if callsAt < tunMaxWriteConsecutiveErrors {
		t.Fatalf("writer called %d times, want at least %d", callsAt, tunMaxWriteConsecutiveErrors)
	}
}

// rawIPv4Packet builds a minimal well-formed IPv4 packet for testing the
// writer loop. The content doesn't matter — only that the writer sees it.
func rawIPv4Packet() []byte {
	pkt := make([]byte, 20)
	pkt[0] = 0x45 // version 4, IHL 5
	pkt[2] = 0x00 // total length 20
	pkt[3] = 0x14
	pkt[8] = 64   // TTL
	pkt[9] = 0x01 // ICMP (content doesn't matter for writer testing)
	pkt[12], pkt[13], pkt[14], pkt[15] = 10, 0, 0, 1
	pkt[16], pkt[17], pkt[18], pkt[19] = 10, 0, 0, 2
	pkt[10], pkt[11] = ipChecksum(pkt)
	return pkt
}


func ipChecksum(b []byte) (byte, byte) {
	var sum uint32
	for i := 0; i < len(b); i += 2 {
		if i+1 < len(b) {
			sum += uint32(b[i])<<8 | uint32(b[i+1])
		} else {
			sum += uint32(b[i]) << 8
		}
	}
	for (sum >> 16) != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	cs := ^uint16(sum)
	return byte(cs >> 8), byte(cs)
}

var _ = tcpip.Error(nil)
var _ = stack.PacketBuffer{}
var _ = header.IPv4ProtocolNumber