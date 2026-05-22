package listener

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// --- UDP header codec tests ---------------------------------------------

func TestParseUDPDatagram(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		wantAdr string
		wantPrt uint16
		wantPay []byte
		wantErr bool
	}{
		{
			name:    "ipv4 + payload",
			input:   []byte{0, 0, 0, 0x01, 1, 2, 3, 4, 0, 53, 'h', 'i'},
			wantAdr: "1.2.3.4",
			wantPrt: 53,
			wantPay: []byte("hi"),
		},
		{
			name: "domain",
			input: append(
				[]byte{0, 0, 0, 0x03, 11},
				append([]byte("example.com"), 0, 53, 'x')...,
			),
			wantAdr: "example.com",
			wantPrt: 53,
			wantPay: []byte("x"),
		},
		{
			name: "ipv6",
			input: append(
				append([]byte{0, 0, 0, 0x04}, make([]byte, 16)...),
				0, 80, 'a', 'b',
			),
			wantAdr: "::",
			wantPrt: 80,
			wantPay: []byte("ab"),
		},
		{
			name:    "fragmented (rejected)",
			input:   []byte{0, 0, 0x01, 0x01, 1, 2, 3, 4, 0, 53},
			wantErr: true,
		},
		{
			name:    "short header",
			input:   []byte{0, 0, 0},
			wantErr: true,
		},
		{
			name:    "bad atyp",
			input:   []byte{0, 0, 0, 0x09, 1, 2, 3, 4, 0, 53},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, pay, err := parseUDPDatagram(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v / %q", h, pay)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if h.addr != tt.wantAdr || h.port != tt.wantPrt {
				t.Errorf("got %s:%d, want %s:%d", h.addr, h.port, tt.wantAdr, tt.wantPrt)
			}
			if !bytes.Equal(pay, tt.wantPay) {
				t.Errorf("payload = %q, want %q", pay, tt.wantPay)
			}
		})
	}
}

func TestEncodeUDPDatagram(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		port    uint16
		payload []byte
		wantHex []byte
	}{
		{
			name: "ipv4",
			host: "1.2.3.4", port: 80, payload: []byte("x"),
			wantHex: []byte{0, 0, 0, 0x01, 1, 2, 3, 4, 0, 80, 'x'},
		},
		{
			name: "domain",
			host: "ex.com", port: 53, payload: []byte("q"),
			wantHex: append([]byte{0, 0, 0, 0x03, 6}, append([]byte("ex.com"), 0, 53, 'q')...),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeUDPDatagram(tt.host, tt.port, tt.payload)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.wantHex) {
				t.Errorf("got %x, want %x", got, tt.wantHex)
			}
		})
	}
}

// --- UDP ASSOCIATE end-to-end --------------------------------------------

// fakePacketConn is a net.PacketConn implemented over channels, standing in
// for a chain-provided protocol.PacketConn during tests.
type fakePacketConn struct {
	toChain   chan fakeDatagram // data client→chain
	fromChain chan fakeDatagram // data chain→client

	readDeadline time.Time
	mu           sync.Mutex
	closed       bool
}

type fakeDatagram struct {
	payload []byte
	addr    net.Addr
}

func newFakePacketConn() *fakePacketConn {
	return &fakePacketConn{
		toChain:   make(chan fakeDatagram, 16),
		fromChain: make(chan fakeDatagram, 16),
	}
}

func (f *fakePacketConn) ReadFrom(buf []byte) (int, net.Addr, error) {
	f.mu.Lock()
	dl := f.readDeadline
	f.mu.Unlock()

	var timeout <-chan time.Time
	if !dl.IsZero() {
		d := time.Until(dl)
		if d <= 0 {
			return 0, nil, timeoutErr{}
		}
		timeout = time.After(d)
	}
	select {
	case dg := <-f.fromChain:
		return copy(buf, dg.payload), dg.addr, nil
	case <-timeout:
		return 0, nil, timeoutErr{}
	}
}

func (f *fakePacketConn) WriteTo(buf []byte, addr net.Addr) (int, error) {
	cp := make([]byte, len(buf))
	copy(cp, buf)
	select {
	case f.toChain <- fakeDatagram{payload: cp, addr: addr}:
		return len(buf), nil
	case <-time.After(time.Second):
		return 0, errors.New("fakePacketConn write backed up")
	}
}

func (f *fakePacketConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}
func (f *fakePacketConn) LocalAddr() net.Addr { return &addrForWrite{host: "fake", port: 0} }
func (f *fakePacketConn) SetDeadline(t time.Time) error {
	f.mu.Lock()
	f.readDeadline = t
	f.mu.Unlock()
	return nil
}
func (f *fakePacketConn) SetReadDeadline(t time.Time) error {
	f.mu.Lock()
	f.readDeadline = t
	f.mu.Unlock()
	return nil
}
func (f *fakePacketConn) SetWriteDeadline(t time.Time) error { return nil }

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestSOCKSv5UDPAssociateRoundTrip(t *testing.T) {
	fake := newFakePacketConn()

	s := &SOCKSv5{
		addr: "127.0.0.1:0",
		dial: unreachable(), // TCP CONNECT not exercised in this test
		dialPacket: func(ctx context.Context, address string) (net.PacketConn, error) {
			return fake, nil
		},
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Open TCP control conn and complete the SOCKS5 handshake.
	control, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	if _, err := control.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	methodReply := make([]byte, 2)
	if _, err := readFull(control, methodReply); err != nil {
		t.Fatal(err)
	}

	// UDP ASSOCIATE request (declare 0.0.0.0:0 as client endpoint).
	req := []byte{0x05, cmdUDPAssociate, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	if _, err := control.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := readFull(control, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != repSuccess {
		t.Fatalf("reply = %v, want success", reply)
	}
	// Extract relay port from reply (BND.PORT is last 2 bytes for IPv4 BND).
	relayPort := binary.BigEndian.Uint16(reply[8:10])
	relayAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(relayPort)}

	// Send a UDP datagram from the client to 8.8.8.8:53 via the relay.
	clientUDP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer clientUDP.Close()

	dg, err := encodeUDPDatagram("8.8.8.8", 53, []byte("DNS QUERY"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clientUDP.WriteToUDP(dg, relayAddr); err != nil {
		t.Fatal(err)
	}

	// The listener should forward the payload to the fake chain.
	select {
	case got := <-fake.toChain:
		if string(got.payload) != "DNS QUERY" {
			t.Errorf("chain got %q, want 'DNS QUERY'", got.payload)
		}
		if got.addr.String() != "8.8.8.8:53" {
			t.Errorf("chain target = %s, want 8.8.8.8:53", got.addr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for client→chain forward")
	}

	// Reply from chain → client.
	fake.fromChain <- fakeDatagram{
		payload: []byte("DNS REPLY"),
		addr:    &addrForWrite{host: "8.8.8.8", port: 53},
	}

	buf := make([]byte, 2048)
	_ = clientUDP.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := clientUDP.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("client UDP read: %v", err)
	}
	h, payload, err := parseUDPDatagram(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if h.addr != "8.8.8.8" || h.port != 53 {
		t.Errorf("reply src = %s:%d, want 8.8.8.8:53", h.addr, h.port)
	}
	if string(payload) != "DNS REPLY" {
		t.Errorf("payload = %q, want 'DNS REPLY'", payload)
	}
}

func TestSOCKSv5UDPAssociateUnsupportedWhenNoPacketDial(t *testing.T) {
	// Listener with dialPacket=nil → UDP ASSOCIATE should be rejected.
	_, addr := newTestListener(t, nil, unreachable())

	control, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	if _, err := control.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	mr := make([]byte, 2)
	if _, err := readFull(control, mr); err != nil {
		t.Fatal(err)
	}

	req := []byte{0x05, cmdUDPAssociate, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	if _, err := control.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := readFull(control, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != repCmdNotSupported {
		t.Errorf("got rep=%#x, want %#x (cmd not supported)", reply[1], repCmdNotSupported)
	}
}
