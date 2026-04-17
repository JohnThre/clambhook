package listener

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// --- codec tests ---------------------------------------------------------

func TestReadMethodSelection(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		want    []byte
		wantErr bool
	}{
		{"no-auth", []byte{0x05, 0x01, 0x00}, []byte{0x00}, false},
		{"both methods", []byte{0x05, 0x02, 0x00, 0x02}, []byte{0x00, 0x02}, false},
		{"bad version", []byte{0x04, 0x01, 0x00}, nil, true},
		{"zero nmethods", []byte{0x05, 0x00}, nil, true},
		{"short methods", []byte{0x05, 0x02, 0x00}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readMethodSelection(bytes.NewReader(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got methods=%v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWriteMethodSelection(t *testing.T) {
	var buf bytes.Buffer
	if err := writeMethodSelection(&buf, methodUserPass); err != nil {
		t.Fatal(err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, []byte{0x05, 0x02}) {
		t.Errorf("got %v, want [5, 2]", got)
	}
}

func TestUserPassAuthRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		user string
		pass string
	}{
		{"normal", "alice", "hunter2"},
		{"empty password", "bob", ""},
		{"long credentials", string(bytes.Repeat([]byte("a"), 200)), "p"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			buf.WriteByte(userPassVer)
			buf.WriteByte(byte(len(tt.user)))
			buf.WriteString(tt.user)
			buf.WriteByte(byte(len(tt.pass)))
			buf.WriteString(tt.pass)

			gotUser, gotPass, err := readUserPassAuth(&buf)
			if err != nil {
				t.Fatal(err)
			}
			if gotUser != tt.user || gotPass != tt.pass {
				t.Errorf("got %q/%q, want %q/%q", gotUser, gotPass, tt.user, tt.pass)
			}
		})
	}
}

func TestReadRequest(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		wantCmd  byte
		wantAddr string
		wantPort uint16
		wantErr  bool
	}{
		{
			"ipv4 connect",
			[]byte{0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1, 0x01, 0xbb},
			0x01, "127.0.0.1", 443, false,
		},
		{
			"domain connect",
			append([]byte{0x05, 0x01, 0x00, 0x03, 11}, append([]byte("example.com"), 0x00, 0x50)...),
			0x01, "example.com", 80, false,
		},
		{
			"ipv6 connect",
			append([]byte{0x05, 0x01, 0x00, 0x04}, append(make([]byte, 16), 0x00, 0x50)...),
			0x01, "::", 80, false,
		},
		{
			"empty domain",
			[]byte{0x05, 0x01, 0x00, 0x03, 0x00},
			0, "", 0, true,
		},
		{
			"unsupported atyp",
			[]byte{0x05, 0x01, 0x00, 0x09},
			0, "", 0, true,
		},
		{
			"bad version",
			[]byte{0x04, 0x01, 0x00, 0x01, 1, 2, 3, 4, 0, 80},
			0, "", 0, true,
		},
	}
	// Pad the ipv6 case: the first [::] needs exactly 16 zero bytes.
	// Handled inline above via make([]byte, 16).
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := readRequest(bytes.NewReader(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got req=%+v", req)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req.cmd != tt.wantCmd || req.addr != tt.wantAddr || req.port != tt.wantPort {
				t.Errorf("got cmd=%#x addr=%s port=%d, want cmd=%#x addr=%s port=%d",
					req.cmd, req.addr, req.port, tt.wantCmd, tt.wantAddr, tt.wantPort)
			}
		})
	}
}

func TestWriteReply(t *testing.T) {
	var buf bytes.Buffer
	if err := writeReply(&buf, repSuccess, "0.0.0.0:0"); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("got %v, want %v", buf.Bytes(), want)
	}
}

func TestRequestTarget(t *testing.T) {
	tests := []struct {
		name string
		req  request
		want string
	}{
		{"ipv4", request{addr: "1.2.3.4", port: 80}, "1.2.3.4:80"},
		{"ipv6", request{addr: "::1", port: 443}, "[::1]:443"},
		{"domain", request{addr: "example.com", port: 8080}, "example.com:8080"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.req.target(); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

// --- end-to-end listener tests ------------------------------------------

// stubDial returns a net.Pipe: the SOCKS5 handler sees one end as "remote",
// the test controls the other end to verify bytes round-trip.
func stubDial(remoteSide chan<- net.Conn) dialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		client, server := net.Pipe()
		remoteSide <- server
		return client, nil
	}
}

// unreachable is used by tests that never expect to reach the dial stage.
func unreachable() dialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, errors.New("unreachable: dial should not have been called")
	}
}

// newTestListener wires a SOCKSv5 with an injected dialer.
func newTestListener(t *testing.T, auth *AuthCreds, dial dialFunc) (*SOCKSv5, string) {
	t.Helper()
	s := &SOCKSv5{
		addr: "127.0.0.1:0",
		auth: auth,
		dial: dial,
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop() })
	return s, s.Addr()
}

func TestSOCKSv5ConnectRoundTrip(t *testing.T) {
	remoteCh := make(chan net.Conn, 1)
	_, addr := newTestListener(t, nil, stubDial(remoteCh))

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()

	// Greeting: VER=5, NMETHODS=1, METHODS=[NoAuth]
	if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 2)
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{0x05, 0x00}) {
		t.Fatalf("method selection got %v, want [5,0]", got)
	}

	// CONNECT to example.com:80
	req := append([]byte{0x05, 0x01, 0x00, 0x03, 11}, []byte("example.com")...)
	req = append(req, 0x00, 0x50)
	if _, err := client.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatal(err)
	}
	if reply[0] != 0x05 || reply[1] != repSuccess {
		t.Fatalf("reply got %v, want ver=5 rep=0", reply)
	}

	remote := <-remoteCh
	defer remote.Close()

	// Client → remote
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(remote, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Errorf("remote got %q, want hello", buf)
	}

	// Remote → client
	if _, err := remote.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(client, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "world" {
		t.Errorf("client got %q, want world", buf)
	}
}

func TestSOCKSv5AuthSuccess(t *testing.T) {
	remoteCh := make(chan net.Conn, 1)
	_, addr := newTestListener(t, &AuthCreds{Username: "u", Password: "p"}, stubDial(remoteCh))

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Offer user/pass.
	if _, err := client.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 2)
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{0x05, 0x02}) {
		t.Fatalf("method got %v, want [5,2]", got)
	}

	// Send credentials.
	if _, err := client.Write([]byte{0x01, 0x01, 'u', 0x01, 'p'}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}
	if got[0] != userPassVer || got[1] != 0x00 {
		t.Fatalf("auth reply got %v, want [1,0]", got)
	}
}

func TestSOCKSv5AuthFailure(t *testing.T) {
	_, addr := newTestListener(t, &AuthCreds{Username: "u", Password: "p"}, unreachable())

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if _, err := client.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 2)
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}

	// Wrong password.
	if _, err := client.Write([]byte{0x01, 0x01, 'u', 0x01, 'X'}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}
	if got[0] != userPassVer || got[1] == 0x00 {
		t.Fatalf("auth reply got %v, want failure (non-zero status)", got)
	}

	// Handler must close the connection after a failed auth.
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	_, err = client.Read(buf)
	if err == nil {
		t.Error("expected connection close after failed auth")
	}
}

func TestSOCKSv5BindRejected(t *testing.T) {
	_, addr := newTestListener(t, nil, unreachable())

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 2)
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}

	// BIND request.
	req := []byte{0x05, 0x02, 0x00, 0x01, 1, 2, 3, 4, 0, 80}
	if _, err := client.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != repCmdNotSupported {
		t.Errorf("got rep=%#x, want %#x (cmd not supported)", reply[1], repCmdNotSupported)
	}
}

func TestSOCKSv5Stop(t *testing.T) {
	s, addr := newTestListener(t, nil, func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, errors.New("no dialer wanted")
	})
	// Stop synchronously.
	if err := s.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// Second Stop is a no-op.
	if err := s.Stop(); err != nil {
		t.Errorf("second stop: %v", err)
	}
	// Port should be released.
	if _, err := net.Dial("tcp", addr); err == nil {
		t.Error("expected dial failure after stop")
	}
}
