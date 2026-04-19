package openvpn

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestBuildKeyMethod2Shape(t *testing.T) {
	cfg := &config{username: "alice", password: "hunter2"}
	msg, err := buildKeyMethod2(cfg, "AES-256-GCM:CHACHA20-POLY1305")
	if err != nil {
		t.Fatal(err)
	}
	// Reserved(4) + key_method(1) + pre_master(48) + random1(32) + random2(32) = 117
	const fixed = 4 + 1 + 48 + 32 + 32
	if len(msg) < fixed {
		t.Fatalf("msg too short: %d", len(msg))
	}
	if msg[0] != 0 || msg[1] != 0 || msg[2] != 0 || msg[3] != 0 {
		t.Errorf("reserved bytes not zero: %v", msg[:4])
	}
	if msg[4] != 0x02 {
		t.Errorf("key_method = %#x, want 2", msg[4])
	}
	// Walk through the four length-prefixed strings. Each lenght field is
	// u16 BE; 0 means "empty".
	off := fixed
	fields := []string{"options", "username", "password", "peer_info"}
	got := make(map[string]string)
	for _, name := range fields {
		if off+2 > len(msg) {
			t.Fatalf("%s: truncated at offset %d", name, off)
		}
		length := binary.BigEndian.Uint16(msg[off : off+2])
		off += 2
		if length == 0 {
			got[name] = ""
			continue
		}
		if off+int(length) > len(msg) {
			t.Fatalf("%s: length %d past end at offset %d", name, length, off)
		}
		got[name] = strings.TrimRight(string(msg[off:off+int(length)]), "\x00")
		off += int(length)
	}
	if off != len(msg) {
		t.Errorf("trailing bytes: %d unconsumed", len(msg)-off)
	}
	if !strings.Contains(got["options"], "cipher AES-256-GCM") {
		t.Errorf("options missing cipher: %q", got["options"])
	}
	if got["username"] != "alice" {
		t.Errorf("username = %q", got["username"])
	}
	if got["password"] != "hunter2" {
		t.Errorf("password = %q", got["password"])
	}
	if !strings.Contains(got["peer_info"], "IV_CIPHERS=AES-256-GCM:CHACHA20-POLY1305") {
		t.Errorf("peer_info missing IV_CIPHERS: %q", got["peer_info"])
	}
	if !strings.Contains(got["peer_info"], "IV_NCP=2") {
		t.Errorf("peer_info missing IV_NCP=2")
	}
}

func TestBuildKeyMethod2EmptyCreds(t *testing.T) {
	cfg := &config{}
	msg, err := buildKeyMethod2(cfg, "AES-256-GCM")
	if err != nil {
		t.Fatal(err)
	}
	// Parse past fixed prefix + options to verify username and password
	// are zero-length.
	off := 117
	optLen := binary.BigEndian.Uint16(msg[off : off+2])
	off += 2 + int(optLen)
	userLen := binary.BigEndian.Uint16(msg[off : off+2])
	if userLen != 0 {
		t.Errorf("expected username_len=0, got %d", userLen)
	}
	off += 2
	passLen := binary.BigEndian.Uint16(msg[off : off+2])
	if passLen != 0 {
		t.Errorf("expected password_len=0, got %d", passLen)
	}
}

func TestAppendLenStringRoundTrip(t *testing.T) {
	cases := []string{"", "a", "hello, world", strings.Repeat("x", 500)}
	for _, s := range cases {
		buf := appendLenString(nil, s)
		if s == "" {
			if !bytes.Equal(buf, []byte{0, 0}) {
				t.Errorf("empty should encode as {0,0}, got %v", buf)
			}
			continue
		}
		length := binary.BigEndian.Uint16(buf[:2])
		if int(length) != len(s)+1 {
			t.Errorf("%q: length = %d, want %d", s, length, len(s)+1)
		}
		if string(buf[2:2+len(s)]) != s {
			t.Errorf("%q: body mismatch", s)
		}
		if buf[len(buf)-1] != 0 {
			t.Errorf("%q: missing trailing NUL", s)
		}
	}
}

func TestParsePushReplyHappyPath(t *testing.T) {
	body := "route-gateway 10.8.0.1,ifconfig 10.8.0.2 10.8.0.1,route 0.0.0.0 0.0.0.0,cipher AES-256-GCM,peer-id 42,dhcp-option DNS 8.8.8.8,dhcp-option DNS 8.8.4.4,tun-mtu 1500,ping 10,ping-restart 60"
	info, err := parsePushReply(body)
	if err != nil {
		t.Fatal(err)
	}
	if info.cipher != "AES-256-GCM" {
		t.Errorf("cipher = %q", info.cipher)
	}
	if info.peerID != 42 {
		t.Errorf("peerID = %d", info.peerID)
	}
	if info.mtu != 1500 {
		t.Errorf("mtu = %d", info.mtu)
	}
	if len(info.addresses) != 1 || info.addresses[0].String() != "10.8.0.2" {
		t.Errorf("addresses = %v", info.addresses)
	}
	if len(info.dnsServers) != 2 {
		t.Errorf("dnsServers = %v", info.dnsServers)
	}
}

func TestParsePushReplyNoIfconfig(t *testing.T) {
	// parsePushReply itself doesn't fail on missing ifconfig (the caller
	// catches it in startNetstack); we just verify it handles sparse
	// replies without crashing.
	info, err := parsePushReply("cipher AES-256-GCM,peer-id 7")
	if err != nil {
		t.Fatal(err)
	}
	if info.cipher != "AES-256-GCM" || info.peerID != 7 {
		t.Errorf("info = %+v", info)
	}
}

func TestParsePushReplyRejectsBadPeerID(t *testing.T) {
	if _, err := parsePushReply("peer-id not_a_number"); err == nil {
		t.Fatal("expected error for non-numeric peer-id")
	}
}

func TestReadLenStringRoundTrip(t *testing.T) {
	enc := appendLenString(nil, "hello")
	s, err := readLenString(bytes.NewReader(enc))
	if err != nil {
		t.Fatal(err)
	}
	if s != "hello" {
		t.Errorf("got %q", s)
	}

	empty, err := readLenString(bytes.NewReader([]byte{0, 0}))
	if err != nil {
		t.Fatal(err)
	}
	if empty != "" {
		t.Errorf("expected empty string, got %q", empty)
	}
}
