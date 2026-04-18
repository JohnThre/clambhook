package shadowsocks

import (
	"bytes"
	"context"
	"crypto/rand"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/clambhook/clambhook/internal/protocol"
	"github.com/clambhook/clambhook/internal/socks"
)

// TestUDPRoundTrip runs a real SS-AEAD UDP echo: the client dials a fake
// server that receives one datagram, parses the address triple out, then
// echoes the payload back with a fresh salt. Covers WriteTo, ReadFrom, and
// per-packet salt randomness in one pass.
func TestUDPRoundTrip(t *testing.T) {
	for _, method := range []string{"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"} {
		t.Run(method, func(t *testing.T) {
			spec, err := cipherByName(method)
			if err != nil {
				t.Skipf("cipher unavailable: %v", err)
			}

			// Stand up a fake SS UDP server on an ephemeral port.
			serverAddr, stop := startFakeUDPServer(t, spec, []byte("secret"))
			defer stop()

			// Build a dialer pointing at the fake server.
			d := &dialer{
				server: protocol.Server{Address: serverAddr.String()},
				cfg: config{
					method:    method,
					password:  "secret",
					spec:      spec,
					masterKey: evpBytesToKey([]byte("secret"), spec.keySize),
				},
			}

			pc, err := d.DialPacket(context.Background(), "")
			if err != nil {
				t.Fatalf("DialPacket: %v", err)
			}
			defer pc.Close()
			_ = pc.SetDeadline(time.Now().Add(2 * time.Second))

			target, _ := net.ResolveUDPAddr("udp", "93.184.216.34:53")
			payload := []byte("hello shadowsocks udp")

			if _, err := pc.WriteTo(payload, target); err != nil {
				t.Fatalf("WriteTo: %v", err)
			}

			reply := make([]byte, 1024)
			n, from, err := pc.ReadFrom(reply)
			if err != nil {
				t.Fatalf("ReadFrom: %v", err)
			}
			if !bytes.Equal(reply[:n], payload) {
				t.Errorf("echo mismatch: got %q want %q", reply[:n], payload)
			}
			if from.String() != target.String() {
				t.Errorf("from addr = %s, want %s", from, target)
			}
		})
	}
}

// TestUDPSaltsAreUnique: send several datagrams and verify each one uses a
// distinct salt. A bug in the random-salt path (e.g. using a fixed seed or
// reusing a buffer) would show up as repeated prefixes on the wire.
func TestUDPSaltsAreUnique(t *testing.T) {
	spec, err := cipherByName("chacha20-ietf-poly1305")
	if err != nil {
		t.Fatal(err)
	}

	// Listen directly (no server-side decryption) and collect raw bytes.
	serverUDP, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer serverUDP.Close()

	d := &dialer{
		server: protocol.Server{Address: serverUDP.LocalAddr().String()},
		cfg: config{
			method:    "chacha20-ietf-poly1305",
			password:  "secret",
			spec:      spec,
			masterKey: evpBytesToKey([]byte("secret"), spec.keySize),
		},
	}

	pc, err := d.DialPacket(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	const count = 16
	target, _ := net.ResolveUDPAddr("udp", "1.2.3.4:80")
	for i := 0; i < count; i++ {
		if _, err := pc.WriteTo([]byte("x"), target); err != nil {
			t.Fatal(err)
		}
	}

	seen := make(map[string]struct{}, count)
	_ = serverUDP.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	for i := 0; i < count; i++ {
		n, _, err := serverUDP.ReadFrom(buf)
		if err != nil {
			t.Fatal(err)
		}
		if n < spec.saltSize {
			t.Fatalf("packet too short: %d bytes", n)
		}
		salt := string(buf[:spec.saltSize])
		if _, dup := seen[salt]; dup {
			t.Fatalf("duplicate salt detected after %d packets", i+1)
		}
		seen[salt] = struct{}{}
	}
}

// TestDialPacketThroughErrors: SS-AEAD UDP can't ride a chained stream.
// Verify the error surfaces rather than being silently ignored.
func TestDialPacketThroughErrors(t *testing.T) {
	d := &dialer{
		cfg: config{method: "chacha20-ietf-poly1305"},
	}
	_, err := d.DialPacketThrough(context.Background(), nil, "example.com:443")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "UDP") {
		t.Errorf("error should mention UDP, got: %v", err)
	}
}

// startFakeUDPServer launches a goroutine that acts as an SS-AEAD UDP peer:
// decrypts one datagram, echoes the payload back with a fresh salt. Returns
// the server's address and a cleanup closer.
func startFakeUDPServer(t *testing.T, spec *cipherSpec, password []byte) (net.Addr, func()) {
	t.Helper()

	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	masterKey := evpBytesToKey(password, spec.keySize)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 65535)
		_ = udp.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, clientAddr, err := udp.ReadFrom(buf)
		if err != nil {
			t.Errorf("server ReadFrom: %v", err)
			return
		}
		wire := buf[:n]

		saltSize := spec.saltSize
		tagSize := spec.tagSize
		if n < saltSize+tagSize {
			t.Errorf("datagram too short: %d", n)
			return
		}
		salt := wire[:saltSize]
		tag := wire[n-tagSize:]
		ct := wire[saltSize : n-tagSize]

		subkey := hkdfSHA1(masterKey, salt, ssSubkeyInfo, spec.keySize)
		zeroNonce := make([]byte, spec.nonceSize)
		pt, err := spec.decrypt(subkey, zeroNonce, ct, nil, tag)
		if err != nil {
			t.Errorf("server decrypt: %v", err)
			return
		}

		addrLen := addrLenFromPlaintext(pt)
		if addrLen < 0 {
			t.Errorf("server parse addr")
			return
		}
		_, _, err = socks.ReadAddr(bytes.NewReader(pt[:addrLen]))
		if err != nil {
			t.Errorf("server read addr: %v", err)
			return
		}
		clientPayload := pt[addrLen:]

		// Echo back: re-wrap [ATYPIPv4 + (spoofed src) + port || payload].
		// Use the original destination as the "from" so the client sees
		// the expected address when it calls ReadFrom.
		replySalt := make([]byte, saltSize)
		if _, err := rand.Read(replySalt); err != nil {
			t.Errorf("server salt: %v", err)
			return
		}
		replySubkey := hkdfSHA1(masterKey, replySalt, ssSubkeyInfo, spec.keySize)

		// Re-serialize the original address triple the client sent to us.
		// For the round-trip test the client sent "93.184.216.34:53", so
		// we need to echo that same address back.
		replyAddr, err := socks.EncodeAddr("93.184.216.34:53")
		if err != nil {
			t.Errorf("server encode reply addr: %v", err)
			return
		}

		replyPT := append(replyAddr, clientPayload...)
		replyCT, replyTag, err := spec.encrypt(replySubkey, zeroNonce, replyPT, nil)
		if err != nil {
			t.Errorf("server encrypt: %v", err)
			return
		}

		replyWire := append(replySalt, replyCT...)
		replyWire = append(replyWire, replyTag...)
		if _, err := udp.WriteTo(replyWire, clientAddr); err != nil {
			t.Errorf("server WriteTo: %v", err)
			return
		}
	}()

	return udp.LocalAddr(), func() {
		udp.Close()
		wg.Wait()
	}
}

func TestAddrLenFromPlaintext(t *testing.T) {
	cases := []struct {
		addr    string
		wantLen int
	}{
		{"1.2.3.4:80", 1 + 4 + 2},
		{"[2001:db8::1]:443", 1 + 16 + 2},
		{"example.com:443", 1 + 1 + 11 + 2},
	}
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			enc, err := socks.EncodeAddr(tc.addr)
			if err != nil {
				t.Fatal(err)
			}
			if got := addrLenFromPlaintext(enc); got != tc.wantLen {
				t.Errorf("got %d want %d", got, tc.wantLen)
			}
		})
	}
	// Unknown ATYP → -1.
	if got := addrLenFromPlaintext([]byte{0x99}); got != -1 {
		t.Errorf("unknown atyp: got %d want -1", got)
	}
}
