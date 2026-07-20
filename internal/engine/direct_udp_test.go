package engine

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

type directUDPTestAddr string

func (a directUDPTestAddr) Network() string { return "udp" }
func (a directUDPTestAddr) String() string  { return string(a) }

func TestDirectUDPRejectsDomainWithoutResolutionOrDial(t *testing.T) {
	pc, err := newDirectPacketConn(context.Background(), "resolver-probe.invalid:53")
	if pc != nil {
		_ = pc.Close()
		t.Fatal("domain target opened a direct UDP socket")
	}
	if err == nil || !strings.Contains(err.Error(), "must be an IP literal") || !strings.Contains(err.Error(), "routed chain") {
		t.Fatalf("newDirectPacketConn error = %v, want clear routed-resolution error", err)
	}

	// A direct session created for a numeric target must also reject a later
	// domain address instead of falling back to net.ResolveUDPAddr.
	pc, err = newDirectPacketConn(context.Background(), "127.0.0.1:53")
	if err != nil {
		t.Fatalf("newDirectPacketConn IP literal: %v", err)
	}
	defer pc.Close()
	if _, err := pc.WriteTo([]byte("query"), directUDPTestAddr("resolver-probe.invalid:53")); err == nil || !strings.Contains(err.Error(), "must be an IP literal") {
		t.Fatalf("WriteTo domain error = %v, want IP-literal rejection", err)
	}
}

func TestDirectUDPIPLiteralRoundTrip(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	target := server.LocalAddr().String()
	pc, err := newDirectPacketConn(context.Background(), target)
	if err != nil {
		t.Fatalf("newDirectPacketConn: %v", err)
	}
	defer pc.Close()

	payload := []byte("direct-udp")
	if _, err := pc.WriteTo(payload, directUDPTestAddr(target)); err != nil {
		t.Fatalf("WriteTo IP literal: %v", err)
	}
	if err := server.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, _, err := server.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	if got := string(buf[:n]); got != string(payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}
