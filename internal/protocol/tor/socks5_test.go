package tor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/socks"
)

// fakeSocks5 plays the server side of a SOCKS5 exchange on the far end of
// a net.Pipe. Call it in a goroutine with the server-side pipe half; it
// scripts a handshake based on opts and then (optionally) echoes bytes.
type fakeSocks5Opts struct {
	requireUserPass bool   // reject no-auth, require user/pass
	expectUser      string // if set, verify user matches
	expectPass      string // if set, verify pass matches
	failAuth        bool   // reply to user/pass with failure
	connectReply    byte   // reply status for CONNECT
	captureReq      *capturedCONNECT
}

type capturedCONNECT struct {
	atyp byte
	host string
	port uint16
}

func fakeSocks5Server(t *testing.T, c net.Conn, opts fakeSocks5Opts) {
	t.Helper()
	defer c.Close()

	// Method greeting: [ver, nmethods, methods...]
	var head [2]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		t.Errorf("server: read greeting head: %v", err)
		return
	}
	if head[0] != 0x05 {
		t.Errorf("server: bad version %#x", head[0])
		return
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		t.Errorf("server: read methods: %v", err)
		return
	}

	selected := byte(0xFF)
	for _, m := range methods {
		if opts.requireUserPass && m == methodUserPass {
			selected = methodUserPass
			break
		}
		if !opts.requireUserPass && m == methodNoAuth {
			selected = methodNoAuth
			break
		}
	}
	if _, err := c.Write([]byte{0x05, selected}); err != nil {
		t.Errorf("server: write method reply: %v", err)
		return
	}
	if selected == methodNone {
		return
	}

	// User/pass sub-negotiation if applicable.
	if selected == methodUserPass {
		var v [2]byte
		if _, err := io.ReadFull(c, v[:]); err != nil {
			t.Errorf("server: read userpass head: %v", err)
			return
		}
		if v[0] != 0x01 {
			t.Errorf("server: bad userpass ver %#x", v[0])
			return
		}
		ulen := int(v[1])
		user := make([]byte, ulen)
		if _, err := io.ReadFull(c, user); err != nil {
			t.Errorf("server: read user: %v", err)
			return
		}
		var pl [1]byte
		if _, err := io.ReadFull(c, pl[:]); err != nil {
			t.Errorf("server: read pass len: %v", err)
			return
		}
		pass := make([]byte, int(pl[0]))
		if _, err := io.ReadFull(c, pass); err != nil {
			t.Errorf("server: read pass: %v", err)
			return
		}
		if opts.expectUser != "" && string(user) != opts.expectUser {
			t.Errorf("server: user = %q, want %q", user, opts.expectUser)
		}
		if opts.expectPass != "" && string(pass) != opts.expectPass {
			t.Errorf("server: pass = %q, want %q", pass, opts.expectPass)
		}
		status := byte(0x00)
		if opts.failAuth {
			status = 0x01
		}
		if _, err := c.Write([]byte{0x01, status}); err != nil {
			t.Errorf("server: write userpass reply: %v", err)
			return
		}
		if opts.failAuth {
			return
		}
	}

	// CONNECT request: [ver, cmd, rsv, atyp, addr, port]
	var reqHead [4]byte
	if _, err := io.ReadFull(c, reqHead[:]); err != nil {
		t.Errorf("server: read CONNECT head: %v", err)
		return
	}
	if reqHead[0] != 0x05 || reqHead[1] != 0x01 || reqHead[2] != 0x00 {
		t.Errorf("server: bad CONNECT prefix %v", reqHead[:3])
		return
	}
	cap := capturedCONNECT{atyp: reqHead[3]}
	switch reqHead[3] {
	case socks.ATYPIPv4:
		var b [4]byte
		io.ReadFull(c, b[:])
		cap.host = net.IP(b[:]).String()
	case socks.ATYPIPv6:
		var b [16]byte
		io.ReadFull(c, b[:])
		cap.host = net.IP(b[:]).String()
	case socks.ATYPDomain:
		var l [1]byte
		io.ReadFull(c, l[:])
		b := make([]byte, int(l[0]))
		io.ReadFull(c, b)
		cap.host = string(b)
	}
	var port [2]byte
	io.ReadFull(c, port[:])
	cap.port = uint16(port[0])<<8 | uint16(port[1])
	if opts.captureReq != nil {
		*opts.captureReq = cap
	}

	// Reply: [ver, rep, rsv, atyp, bnd.addr, bnd.port]. Use a zero IPv4
	// BND.ADDR which matches what Tor typically sends.
	reply := []byte{0x05, opts.connectReply, 0x00, socks.ATYPIPv4, 0, 0, 0, 0, 0, 0}
	if _, err := c.Write(reply); err != nil {
		t.Errorf("server: write CONNECT reply: %v", err)
	}
}

func TestSocks5ConnectNoAuthSuccess(t *testing.T) {
	client, server := net.Pipe()
	var captured capturedCONNECT
	done := make(chan struct{})
	go func() {
		fakeSocks5Server(t, server, fakeSocks5Opts{connectReply: 0x00, captureReq: &captured})
		close(done)
	}()

	if err := socks5Connect(client, "example.com:443", "", ""); err != nil {
		t.Fatalf("socks5Connect: %v", err)
	}
	client.Close()
	<-done

	if captured.atyp != socks.ATYPDomain {
		t.Fatalf("atyp = %#x, want ATYPDomain", captured.atyp)
	}
	if captured.host != "example.com" {
		t.Fatalf("host = %q", captured.host)
	}
	if captured.port != 443 {
		t.Fatalf("port = %d", captured.port)
	}
}

func TestSocks5ConnectOnionDomain(t *testing.T) {
	client, server := net.Pipe()
	var captured capturedCONNECT
	done := make(chan struct{})
	go func() {
		fakeSocks5Server(t, server, fakeSocks5Opts{connectReply: 0x00, captureReq: &captured})
		close(done)
	}()

	onion := "duckduckgogg42xjoc72x3sjasowoarfbgcmvfimaftt6twagswzczad.onion"
	if err := socks5Connect(client, onion+":80", "", ""); err != nil {
		t.Fatalf("socks5Connect: %v", err)
	}
	client.Close()
	<-done

	if captured.atyp != socks.ATYPDomain {
		t.Fatalf("onion must be sent as ATYPDomain (Tor resolves in-circuit), got atyp=%#x", captured.atyp)
	}
	if captured.host != onion {
		t.Fatalf("onion host = %q, want %q", captured.host, onion)
	}
}

func TestSocks5ConnectUserPassSuccess(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		fakeSocks5Server(t, server, fakeSocks5Opts{
			requireUserPass: true,
			expectUser:      "clambhook",
			expectPass:      "profile-1",
			connectReply:    0x00,
		})
		close(done)
	}()

	if err := socks5Connect(client, "example.com:443", "clambhook", "profile-1"); err != nil {
		t.Fatalf("socks5Connect: %v", err)
	}
	client.Close()
	<-done
}

func TestSocks5ConnectUserPassFailure(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		fakeSocks5Server(t, server, fakeSocks5Opts{
			requireUserPass: true,
			failAuth:        true,
		})
		close(done)
	}()

	err := socks5Connect(client, "example.com:443", "clambhook", "wrong")
	client.Close()
	<-done
	if err == nil {
		t.Fatal("expected auth failure")
	}
	if !strings.Contains(err.Error(), "userpass auth failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSocks5ConnectReplyCodeMapping(t *testing.T) {
	cases := []struct {
		code byte
		want string
	}{
		{0x01, "general SOCKS server failure"},
		{0x03, "network unreachable"},
		{0x04, "host unreachable"},
		{0x05, "connection refused"},
		{0x08, "address type not supported"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			client, server := net.Pipe()
			done := make(chan struct{})
			go func() {
				fakeSocks5Server(t, server, fakeSocks5Opts{connectReply: tc.code})
				close(done)
			}()
			err := socks5Connect(client, "example.com:443", "", "")
			client.Close()
			<-done
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want contains %q", err, tc.want)
			}
		})
	}
}

func TestSocks5ConnectNoMethodsAccepted(t *testing.T) {
	// Server offers only user/pass; client offers only no-auth → 0xFF.
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		fakeSocks5Server(t, server, fakeSocks5Opts{requireUserPass: true})
		close(done)
	}()

	err := socks5Connect(client, "example.com:443", "", "")
	client.Close()
	<-done
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !strings.Contains(err.Error(), "rejected all offered methods") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSocks5ConnectGreetingShape pins the exact bytes of the method
// greeting for both the no-auth and user/pass cases. If this test fails,
// we've changed the wire format — intentional or not, it's worth a
// second look.
func TestSocks5ConnectGreetingShape(t *testing.T) {
	var buf bytes.Buffer
	if err := writeMethodGreeting(&buf, false); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), []byte{0x05, 0x01, 0x00}) {
		t.Fatalf("no-auth greeting = % x", buf.Bytes())
	}
	buf.Reset()
	if err := writeMethodGreeting(&buf, true); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), []byte{0x05, 0x02, 0x00, 0x02}) {
		t.Fatalf("user/pass greeting = % x", buf.Bytes())
	}
}

// TestDialThroughUsesUnderlying proves that DialThrough sends the SOCKS5
// handshake over the underlying stream (not a fresh TCP dial) and returns
// a usable protocol.Conn afterwards.
func TestDialThroughUsesUnderlying(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		fakeSocks5Server(t, server, fakeSocks5Opts{connectReply: 0x00})
		close(done)
	}()

	d := &dialer{cfg: config{socksAddr: "unused:0"}}
	c, err := d.DialThrough(context.Background(), client, "example.com:443")
	if err != nil {
		t.Fatalf("DialThrough: %v", err)
	}
	if c.Protocol() != "tor" {
		t.Fatalf("Protocol() = %q", c.Protocol())
	}
	c.Close()
	<-done
}

// Safety net: confirm we don't swallow io.EOF when the server abruptly
// closes mid-handshake. The exact error text doesn't matter; what matters
// is we see *an* error rather than pretending the handshake succeeded.
func TestSocks5ConnectShortRead(t *testing.T) {
	client, server := net.Pipe()
	go func() {
		// Read greeting but close before replying.
		var head [2]byte
		io.ReadFull(server, head[:])
		methods := make([]byte, int(head[1]))
		io.ReadFull(server, methods)
		server.Close()
	}()

	err := socks5Connect(client, "example.com:443", "", "")
	client.Close()
	if err == nil {
		t.Fatal("expected error on short read")
	}
	if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) && !strings.Contains(err.Error(), "read method reply") {
		t.Fatalf("unexpected error: %v", err)
	}
}
