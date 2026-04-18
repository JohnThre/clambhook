package shadowsocks

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/clambhook/clambhook/internal/protocol"
	"github.com/clambhook/clambhook/internal/socks"
)

func TestParseConfigMissingMethod(t *testing.T) {
	_, err := parseConfig(protocol.Server{
		Address:  "example.com:8388",
		Settings: map[string]any{"password": "secret"},
	})
	if err == nil || !strings.Contains(err.Error(), "method") {
		t.Fatalf("expected method-required error, got %v", err)
	}
}

func TestParseConfigMissingPassword(t *testing.T) {
	_, err := parseConfig(protocol.Server{
		Address:  "example.com:8388",
		Settings: map[string]any{"method": "chacha20-ietf-poly1305"},
	})
	if err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("expected password-required error, got %v", err)
	}
}

func TestParseConfigRejectsLegacy(t *testing.T) {
	_, err := parseConfig(protocol.Server{
		Address: "example.com:8388",
		Settings: map[string]any{
			"method":   "rc4-md5",
			"password": "secret",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "insecure") {
		t.Fatalf("expected legacy-cipher error, got %v", err)
	}
}

func TestParseConfigDerivesMasterKey(t *testing.T) {
	cfg, err := parseConfig(protocol.Server{
		Address: "example.com:8388",
		Settings: map[string]any{
			"method":   "chacha20-ietf-poly1305",
			"password": "secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.masterKey) != 32 {
		t.Errorf("master key len = %d, want 32", len(cfg.masterKey))
	}
	// Master key must be deterministic for a given password+method.
	got := evpBytesToKey([]byte("secret"), 32)
	if !bytes.Equal(cfg.masterKey, got) {
		t.Error("master key doesn't match evpBytesToKey output")
	}
}

// TestTCPRoundTrip sets up a fake SS server on one end of a net.Pipe: it
// reads the client's salt, decodes the address, echoes a greeting plus the
// client's data back through its own SS framing, and closes. Exercises the
// full protocol including address parsing and bidirectional chunked streaming.
func TestTCPRoundTrip(t *testing.T) {
	for _, method := range []string{"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"} {
		t.Run(method, func(t *testing.T) {
			spec, err := cipherByName(method)
			if err != nil {
				t.Skipf("cipher unavailable: %v", err)
			}

			clientSide, serverSide := net.Pipe()

			d := &dialer{
				server: protocol.Server{Address: "test:8388"},
				cfg: config{
					method:    method,
					password:  "secret",
					spec:      spec,
					masterKey: evpBytesToKey([]byte("secret"), spec.keySize),
				},
			}

			// The fake server: read salt → derive subkey → decrypt first
			// chunk as address → decrypt subsequent chunks as data → echo
			// back via its own SS-encrypted frame.
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer serverSide.Close()
				runFakeServer(t, serverSide, d.cfg.masterKey, spec)
			}()

			conn, err := d.handshake(clientSide, "example.com:443")
			if err != nil {
				t.Fatalf("handshake: %v", err)
			}
			defer conn.Close()

			payload := []byte("hello shadowsocks")
			if _, err := conn.Write(payload); err != nil {
				t.Fatalf("write: %v", err)
			}

			// Read server's echo.
			reply := make([]byte, 1024)
			n, err := conn.Read(reply)
			if err != nil && err != io.EOF {
				t.Fatalf("read: %v", err)
			}
			if !bytes.Equal(reply[:n], payload) {
				t.Errorf("echo mismatch: got %q want %q", reply[:n], payload)
			}

			wg.Wait()
		})
	}
}

// runFakeServer speaks SS-AEAD from the server's perspective: reads salt,
// derives the subkey from the SAME master key the client used, reads the
// address chunk, then echoes any subsequent payload back in its own
// independently-salted direction.
func runFakeServer(t *testing.T, conn net.Conn, masterKey []byte, spec *cipherSpec) {
	t.Helper()

	// Read client's salt (first bytes on the wire).
	clientSalt := make([]byte, spec.saltSize)
	if _, err := io.ReadFull(conn, clientSalt); err != nil {
		t.Errorf("server read client salt: %v", err)
		return
	}
	clientSubkey := hkdfSHA1(masterKey, clientSalt, ssSubkeyInfo, spec.keySize)
	sr := newStreamReader(conn, spec, clientSubkey)

	// First chunk = address triple.
	addrBuf := make([]byte, 1024)
	n, err := sr.Read(addrBuf)
	if err != nil {
		t.Errorf("server read address: %v", err)
		return
	}
	host, port, err := socks.ReadAddr(bytes.NewReader(addrBuf[:n]))
	if err != nil {
		t.Errorf("server parse address: %v", err)
		return
	}
	if host != "example.com" || port != 443 {
		t.Errorf("server got unexpected target: %s:%d", host, port)
		return
	}

	// Read client payload (may ride its own chunk, or tail of the addr chunk
	// if the caller coalesced — we don't, but be tolerant).
	payloadBuf := make([]byte, 1024)
	n2, err := sr.Read(payloadBuf)
	if err != nil {
		t.Errorf("server read payload: %v", err)
		return
	}

	// Echo back through server's own salt/subkey (fresh CSPRNG, different direction).
	serverSalt := make([]byte, spec.saltSize)
	for i := range serverSalt {
		serverSalt[i] = byte(i + 1) // deterministic for test, but distinct from client
	}
	if _, err := conn.Write(serverSalt); err != nil {
		t.Errorf("server write salt: %v", err)
		return
	}
	serverSubkey := hkdfSHA1(masterKey, serverSalt, ssSubkeyInfo, spec.keySize)
	sw := newStreamWriter(conn, spec, serverSubkey)
	if _, err := sw.Write(payloadBuf[:n2]); err != nil {
		t.Errorf("server write echo: %v", err)
		return
	}
}

// TestDialerSatisfiesInterface is a compile-time-ish check that the SS
// dialer still plugs into the protocol registry correctly.
func TestDialerSatisfiesInterface(t *testing.T) {
	var _ protocol.Dialer = (*dialer)(nil)

	// The stub Dial should still error when given an unreachable address
	// (sanity: the factory path works end-to-end, parseConfig is called).
	d, err := protocol.NewDialer(protocol.Server{
		Name:     "test",
		Address:  "127.0.0.1:0",
		Protocol: "shadowsocks",
		Settings: map[string]any{
			"method":   "chacha20-ietf-poly1305",
			"password": "secret",
		},
	})
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	if d.Protocol() != "shadowsocks" {
		t.Errorf("Protocol() = %q, want shadowsocks", d.Protocol())
	}

	// Actual Dial to port 0 must fail (the point is that we call into the
	// real code path, not that a connection is established).
	_, err = d.Dial(context.Background(), "tcp", "example.com:443")
	if err == nil {
		t.Error("expected dial error to unreachable address")
	}
}
