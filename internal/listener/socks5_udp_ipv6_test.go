package listener

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestSOCKSv5UDPAssociateIPv6RoundTrip(t *testing.T) {
	probe, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	_ = probe.Close()

	fake := newFakePacketConn()
	s := &SOCKSv5{
		addr: "[::1]:0",
		dial: unreachable(),
		dialPacket: func(context.Context, string) (net.PacketConn, error) {
			return fake, nil
		},
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("start IPv6 SOCKS5: %v", err)
	}
	defer s.Stop()

	control, err := net.Dial("tcp6", s.Addr())
	if err != nil {
		t.Fatalf("dial IPv6 control: %v", err)
	}
	defer control.Close()
	if _, err := control.Write([]byte{0x05, 0x01, methodNoAuth}); err != nil {
		t.Fatal(err)
	}
	methodReply := make([]byte, 2)
	if _, err := io.ReadFull(control, methodReply); err != nil {
		t.Fatal(err)
	}
	if methodReply[1] != methodNoAuth {
		t.Fatalf("method reply = %v", methodReply)
	}

	// Declare the IPv6 unspecified endpoint. The server latches the real UDP
	// source from the first datagram, as allowed by RFC 1928.
	req := make([]byte, 22)
	req[0], req[1], req[2], req[3] = socks5Version, cmdUDPAssociate, 0, atypIPv6
	if _, err := control.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 22)
	if _, err := io.ReadFull(control, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != repSuccess || reply[3] != atypIPv6 {
		t.Fatalf("UDP ASSOCIATE reply = %v, want IPv6 success", reply)
	}
	relayIP := net.IP(reply[4:20])
	if !relayIP.Equal(net.IPv6loopback) {
		t.Fatalf("BND.ADDR = %s, want ::1", relayIP)
	}
	relayAddr := &net.UDPAddr{IP: relayIP, Port: int(binary.BigEndian.Uint16(reply[20:22]))}

	client, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback, Port: 0})
	if err != nil {
		t.Fatalf("listen IPv6 UDP client: %v", err)
	}
	defer client.Close()
	datagram, err := encodeUDPDatagram("2001:db8::53", 53, []byte("query-v6"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.WriteToUDP(datagram, relayAddr); err != nil {
		t.Fatalf("write IPv6 relay: %v", err)
	}

	select {
	case got := <-fake.toChain:
		if string(got.payload) != "query-v6" || got.addr.String() != "[2001:db8::53]:53" {
			t.Fatalf("chain datagram = %q to %s", got.payload, got.addr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for IPv6 client-to-chain datagram")
	}

	fake.fromChain <- fakeDatagram{
		payload: []byte("reply-v6"),
		addr:    &addrForWrite{host: "2001:db8::53", port: 53},
	}
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, _, err := client.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read IPv6 relay reply: %v", err)
	}
	hdr, payload, err := parseUDPDatagram(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if hdr.addr != "2001:db8::53" || hdr.port != 53 || string(payload) != "reply-v6" {
		t.Fatalf("reply = [%s]:%d %q", hdr.addr, hdr.port, payload)
	}
}
