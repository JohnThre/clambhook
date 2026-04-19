package listener

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestHTTPListener mirrors newTestListener (socks5) but for HTTP. The
// listener binds 127.0.0.1:0 and accepts a stub dialer so tests can play
// the "remote" side with a net.Pipe.
func newTestHTTPListener(t *testing.T, auth *AuthCreds, dial dialFunc) (*HTTP, string) {
	t.Helper()
	s := &HTTP{
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

// newTestHTTPListenerWithOpts lets tests exercise options like MaxConnections.
func newTestHTTPListenerWithOpts(t *testing.T, dial dialFunc, opts Options) (*HTTP, string) {
	t.Helper()
	s := &HTTP{
		addr: "127.0.0.1:0",
		dial: dial,
		opts: opts,
	}
	if opts.MaxConnections > 0 {
		s.sem = make(chan struct{}, opts.MaxConnections)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop() })
	return s, s.Addr()
}

// readStatusLine reads one HTTP status line "<proto> <code> <reason>\r\n".
// Tests use it to assert error-path responses without pulling in a full
// response parser.
func readStatusLine(t *testing.T, r io.Reader) (proto string, code int, reason string) {
	t.Helper()
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status line: %v", err)
	}
	line = strings.TrimRight(line, "\r\n")
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 3 {
		t.Fatalf("malformed status line: %q", line)
	}
	proto = parts[0]
	if _, err := fmt.Sscanf(parts[1], "%d", &code); err != nil {
		t.Fatalf("parse code: %v", err)
	}
	reason = parts[2]
	return
}

// --- CONNECT tests -------------------------------------------------------

func TestHTTPConnectRoundTrip(t *testing.T) {
	remoteCh := make(chan net.Conn, 1)
	_, addr := newTestHTTPListener(t, nil, stubDial(remoteCh))

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()

	if _, err := io.WriteString(client,
		"CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatal(err)
	}

	br := bufio.NewReader(client)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status got %d, want 200", resp.StatusCode)
	}

	remote := <-remoteCh
	defer remote.Close()

	// After the 200, reads must come through the bufio since the bufio
	// may have prefetched past the \r\n\r\n boundary. Clients don't care,
	// but the test reader switches to bufio to mirror what the handler does.
	// Client → remote.
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

	// Remote → client.
	if _, err := remote.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "world" {
		t.Errorf("client got %q, want world", buf)
	}
}

func TestHTTPConnectEarlyData(t *testing.T) {
	remoteCh := make(chan net.Conn, 1)
	_, addr := newTestHTTPListener(t, nil, stubDial(remoteCh))

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()

	// Write CONNECT headers + early client data in a single call. The
	// bufio inside the handler will prefetch the early bytes along with
	// the headers. The handler must flush those bytes to the remote via
	// the bufReadConn shim, not drop them.
	payload := "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n" + "EARLY"
	if _, err := io.WriteString(client, payload); err != nil {
		t.Fatal(err)
	}

	br := bufio.NewReader(client)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status got %d, want 200", resp.StatusCode)
	}

	remote := <-remoteCh
	defer remote.Close()

	buf := make([]byte, 5)
	if _, err := io.ReadFull(remote, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "EARLY" {
		t.Errorf("remote got %q, want EARLY", buf)
	}
}

func TestHTTPConnectAuthMissing(t *testing.T) {
	_, addr := newTestHTTPListener(t,
		&AuthCreds{Username: "alice", Password: "hunter2"}, unreachable())

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()

	if _, err := io.WriteString(client,
		"CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(client)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 407 {
		t.Fatalf("status got %d, want 407", resp.StatusCode)
	}
	if got := resp.Header.Get("Proxy-Authenticate"); !strings.Contains(got, "Basic") {
		t.Errorf("Proxy-Authenticate got %q, want Basic challenge", got)
	}
}

func TestHTTPConnectAuthWrong(t *testing.T) {
	_, addr := newTestHTTPListener(t,
		&AuthCreds{Username: "alice", Password: "hunter2"}, unreachable())

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()

	bad := base64.StdEncoding.EncodeToString([]byte("alice:wrong"))
	payload := "CONNECT example.com:443 HTTP/1.1\r\n" +
		"Host: example.com:443\r\n" +
		"Proxy-Authorization: Basic " + bad + "\r\n\r\n"
	if _, err := io.WriteString(client, payload); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(client)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 407 {
		t.Fatalf("status got %d, want 407", resp.StatusCode)
	}
}

func TestHTTPConnectAuthOK(t *testing.T) {
	remoteCh := make(chan net.Conn, 1)
	_, addr := newTestHTTPListener(t,
		&AuthCreds{Username: "alice", Password: "hunter2"}, stubDial(remoteCh))

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()

	ok := base64.StdEncoding.EncodeToString([]byte("alice:hunter2"))
	payload := "CONNECT example.com:443 HTTP/1.1\r\n" +
		"Host: example.com:443\r\n" +
		"Proxy-Authorization: Basic " + ok + "\r\n\r\n"
	if _, err := io.WriteString(client, payload); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(client)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status got %d, want 200", resp.StatusCode)
	}
	// Drain the stub remote so the handler can unwind cleanly.
	remote := <-remoteCh
	_ = remote.Close()
}

func TestHTTPConnectBadTarget(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name: "missing port",
			payload: "CONNECT example.com HTTP/1.1\r\n" +
				"Host: example.com\r\n\r\n",
		},
		{
			name: "body on connect",
			payload: "CONNECT example.com:443 HTTP/1.1\r\n" +
				"Host: example.com:443\r\nContent-Length: 5\r\n\r\nhello",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, addr := newTestHTTPListener(t, nil, unreachable())
			client, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer client.Close()
			if _, err := io.WriteString(client, tt.payload); err != nil {
				t.Fatal(err)
			}
			br := bufio.NewReader(client)
			resp, err := http.ReadResponse(br, nil)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != 400 {
				t.Fatalf("status got %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestHTTPConnectIPv6(t *testing.T) {
	var seenAddr atomic.Value
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		seenAddr.Store(address)
		_, server := net.Pipe()
		return server, nil
	}
	_, addr := newTestHTTPListener(t, nil, dial)

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()

	if _, err := io.WriteString(client,
		"CONNECT [::1]:443 HTTP/1.1\r\nHost: [::1]:443\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(client)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status got %d, want 200", resp.StatusCode)
	}
	if got := seenAddr.Load(); got != "[::1]:443" {
		t.Errorf("dial target got %q, want [::1]:443", got)
	}
}

// --- Forward tests -------------------------------------------------------

func TestHTTPForwardGET(t *testing.T) {
	remoteCh := make(chan net.Conn, 1)
	_, addr := newTestHTTPListener(t, nil, stubDial(remoteCh))

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()

	// Origin-side goroutine: parse the forwarded request, assert it's in
	// origin form with hop-by-hop headers stripped, then respond.
	errCh := make(chan error, 1)
	go func() {
		remote := <-remoteCh
		defer remote.Close()

		req, err := http.ReadRequest(bufio.NewReader(remote))
		if err != nil {
			errCh <- fmt.Errorf("origin read request: %w", err)
			return
		}
		if req.Method != "GET" {
			errCh <- fmt.Errorf("method got %q, want GET", req.Method)
			return
		}
		// Origin form: path only, no scheme/host in request-URI.
		if req.RequestURI != "/path?x=1" {
			errCh <- fmt.Errorf("request-URI got %q, want /path?x=1", req.RequestURI)
			return
		}
		if req.Host != "example.com" {
			errCh <- fmt.Errorf("Host got %q, want example.com", req.Host)
			return
		}
		if got := req.Header.Get("Proxy-Connection"); got != "" {
			errCh <- fmt.Errorf("Proxy-Connection leaked through: %q", got)
			return
		}
		if got := req.Header.Get("Proxy-Authorization"); got != "" {
			errCh <- fmt.Errorf("Proxy-Authorization leaked through: %q", got)
			return
		}
		// Write a response.
		if _, err := io.WriteString(remote,
			"HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"); err != nil {
			errCh <- fmt.Errorf("origin write: %w", err)
			return
		}
		errCh <- nil
	}()

	payload := "GET http://example.com/path?x=1 HTTP/1.1\r\n" +
		"Host: example.com\r\n" +
		"Proxy-Connection: keep-alive\r\n" +
		"User-Agent: test\r\n\r\n"
	if _, err := io.WriteString(client, payload); err != nil {
		t.Fatal(err)
	}

	br := bufio.NewReader(client)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status got %d, want 200", resp.StatusCode)
	}
	if string(body) != "hello" {
		t.Errorf("body got %q, want hello", body)
	}

	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestHTTPForwardPOST(t *testing.T) {
	remoteCh := make(chan net.Conn, 1)
	_, addr := newTestHTTPListener(t, nil, stubDial(remoteCh))

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()

	errCh := make(chan error, 1)
	go func() {
		remote := <-remoteCh
		defer remote.Close()
		req, err := http.ReadRequest(bufio.NewReader(remote))
		if err != nil {
			errCh <- fmt.Errorf("origin read: %w", err)
			return
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			errCh <- fmt.Errorf("origin read body: %w", err)
			return
		}
		if string(body) != "payload-data" {
			errCh <- fmt.Errorf("body got %q, want payload-data", body)
			return
		}
		if _, err := io.WriteString(remote,
			"HTTP/1.1 201 Created\r\nContent-Length: 0\r\n\r\n"); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	payload := "POST http://example.com/upload HTTP/1.1\r\n" +
		"Host: example.com\r\n" +
		"Content-Length: 12\r\n\r\n" +
		"payload-data"
	if _, err := io.WriteString(client, payload); err != nil {
		t.Fatal(err)
	}

	br := bufio.NewReader(client)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("status got %d, want 201", resp.StatusCode)
	}

	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestHTTPForwardRejectsHTTPS(t *testing.T) {
	_, addr := newTestHTTPListener(t, nil, unreachable())

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	if _, err := io.WriteString(client,
		"GET https://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(client)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status got %d, want 400", resp.StatusCode)
	}
}

func TestHTTPForwardAuthMissing(t *testing.T) {
	_, addr := newTestHTTPListener(t,
		&AuthCreds{Username: "alice", Password: "hunter2"}, unreachable())

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	if _, err := io.WriteString(client,
		"GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(client)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 407 {
		t.Fatalf("status got %d, want 407", resp.StatusCode)
	}
}

// --- Security & lifecycle tests -----------------------------------------

func TestHTTPHeaderSizeLimit(t *testing.T) {
	_, addr := newTestHTTPListener(t, nil, unreachable())

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	// Bound the whole exchange: once the cap trips the listener stops
	// reading, which can back-pressure client writes. SetDeadline covers
	// both sides so the test can't hang if the cap is broken.
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))

	// Send a request-line then a single header whose value is larger than
	// the 1 MiB cap. The listener must close without OOM-ing and should
	// respond with 400 (or EOF if the cap tripped mid-parse).
	if _, err := io.WriteString(client,
		"GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\nX-Big: "); err != nil {
		t.Fatal(err)
	}
	blob := strings.Repeat("A", 2<<20) // 2 MiB
	_, _ = io.WriteString(client, blob)
	_, _ = io.WriteString(client, "\r\n\r\n")

	// Either we get a 400 back, or the listener hangs up (EOF). Both are
	// acceptable evidence that the cap fired.
	br := bufio.NewReader(client)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return // EOF / reset is acceptable
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status got %d, want 400 (or EOF)", resp.StatusCode)
	}
}

func TestHTTPStopDrainsInFlight(t *testing.T) {
	remoteCh := make(chan net.Conn, 1)
	s, addr := newTestHTTPListener(t, nil, stubDial(remoteCh))

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	if _, err := io.WriteString(client,
		"CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(client)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status got %d, want 200", resp.StatusCode)
	}
	remote := <-remoteCh
	defer remote.Close()

	// Baseline: at least one handler in flight.
	deadline := time.Now().Add(2 * time.Second)
	for s.ActiveConns() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.ActiveConns() == 0 {
		t.Fatal("expected an in-flight handler")
	}

	// Stop must return within stopGrace — the watchdog closes the conns
	// on ctx cancel, unblocking the relay.
	start := time.Now()
	if err := s.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if elapsed := time.Since(start); elapsed > stopGrace+500*time.Millisecond {
		t.Fatalf("Stop took %v, want ≤ stopGrace (%v)", elapsed, stopGrace)
	}
}

func TestHTTPMaxConnectionsSemaphore(t *testing.T) {
	remoteCh := make(chan net.Conn, 8)
	// Dialer always returns a pipe — remote side is drained asynchronously.
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		c, s := net.Pipe()
		remoteCh <- s
		return c, nil
	}
	s, addr := newTestHTTPListenerWithOpts(t, dial,
		Options{MaxConnections: 1, HandshakeTimeout: 2 * time.Second})

	// Fire the first CONNECT and keep it open.
	c1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer c1.Close()
	if _, err := io.WriteString(c1,
		"CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	br1 := bufio.NewReader(c1)
	if _, err := http.ReadResponse(br1, nil); err != nil {
		t.Fatalf("read response 1: %v", err)
	}

	// Wait until the handler is in flight.
	deadline := time.Now().Add(2 * time.Second)
	for s.ActiveConns() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.ActiveConns() != 1 {
		t.Fatalf("ActiveConns got %d, want 1", s.ActiveConns())
	}

	// Second dial can establish TCP, but the handler goroutine blocks on
	// the semaphore and never parses the request. We rely on a brief wait
	// — if the cap were ignored, ActiveConns would climb to 2.
	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer c2.Close()
	time.Sleep(100 * time.Millisecond)
	if got := s.ActiveConns(); got > 1 {
		t.Fatalf("ActiveConns got %d, want 1 (cap enforced)", got)
	}
}
