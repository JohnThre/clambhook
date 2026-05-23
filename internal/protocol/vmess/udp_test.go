package vmess

import (
	"bytes"
	"net"
	"testing"
)

func TestPacketCommandAutoUsesXUDPForUnboundSession(t *testing.T) {
	d := &dialer{cfg: config{packetEncoding: packetEncodingAuto}}
	cmd, xudp, err := d.packetCommand("")
	if err != nil {
		t.Fatal(err)
	}
	if cmd != cmdMux || !xudp {
		t.Fatalf("packetCommand(empty) = cmd %#x xudp %v, want cmdMux true", cmd, xudp)
	}

	cmd, xudp, err = d.packetCommand("1.1.1.1:53")
	if err != nil {
		t.Fatal(err)
	}
	if cmd != cmdUDP || xudp {
		t.Fatalf("packetCommand(target) = cmd %#x xudp %v, want cmdUDP false", cmd, xudp)
	}
}

func TestXUDPFrameRoundTripPerDatagramDestinations(t *testing.T) {
	addr1, err := net.ResolveUDPAddr("udp", "1.2.3.4:53")
	if err != nil {
		t.Fatal(err)
	}
	frame1, err := encodeXUDPFrame([]byte("dns-a"), addr1, true)
	if err != nil {
		t.Fatal(err)
	}
	if frame1[4] != xudpStatusNew {
		t.Fatalf("first frame status = %#x, want new", frame1[4])
	}
	frameLen := int(frame1[0])<<8 | int(frame1[1])
	target, err := readXUDPAddr(bytes.NewReader(frame1[7 : 2+frameLen]))
	if err != nil {
		t.Fatal(err)
	}
	if target != "1.2.3.4:53" {
		t.Fatalf("first target = %q, want 1.2.3.4:53", target)
	}
	payloadOff := 2 + frameLen
	payloadLen := int(frame1[payloadOff])<<8 | int(frame1[payloadOff+1])
	payload := frame1[payloadOff+2 : payloadOff+2+payloadLen]
	if !bytes.Equal(payload, []byte("dns-a")) {
		t.Fatalf("first payload = %q", payload)
	}

	frame2, err := encodeXUDPFrame([]byte("dns-b"), packetAddr{target: "example.com:853"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if frame2[4] != xudpStatusKeep {
		t.Fatalf("second frame status = %#x, want keep", frame2[4])
	}
	payload, target, ok, err := decodeXUDPFrame(frame2, "")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("second frame had no payload")
	}
	if target != "example.com:853" {
		t.Fatalf("second target = %q, want example.com:853", target)
	}
	if !bytes.Equal(payload, []byte("dns-b")) {
		t.Fatalf("second payload = %q", payload)
	}
}

func TestXUDPFrameUsesDefaultTargetWhenReplyOmitsAddress(t *testing.T) {
	var frame []byte
	frame = append(frame, 0x00, 0x04) // session ID + status + option only
	frame = append(frame, 0x00, 0x00, xudpStatusKeep, xudpOptionData)
	frame = append(frame, 0x00, 0x04)
	frame = append(frame, []byte("pong")...)

	payload, target, ok, err := decodeXUDPFrame(frame, "9.9.9.9:53")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("frame had no payload")
	}
	if target != "9.9.9.9:53" {
		t.Fatalf("target = %q, want default", target)
	}
	if !bytes.Equal(payload, []byte("pong")) {
		t.Fatalf("payload = %q", payload)
	}
}
