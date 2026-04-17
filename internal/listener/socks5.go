package listener

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/clambhook/clambhook/internal/chain"
)

// AuthCreds carries optional username/password credentials. When nil, the
// listener accepts only METHOD 0x00 (no-auth). When non-nil, it accepts only
// METHOD 0x02 (user/pass) — mixing is possible per RFC 1928 but we keep
// configuration binary for clarity.
type AuthCreds struct {
	Username string
	Password string
}

// Options tunes per-listener runtime behavior. Zero values mean "use the
// default" for each field — callers can pass a zero-valued Options and get
// sensible behavior.
type Options struct {
	// MaxConnections caps concurrent in-flight client handlers. 0 means
	// unlimited (the previous behavior). When the ceiling is hit, new
	// accepts are held until an existing handler finishes.
	MaxConnections int

	// HandshakeTimeout is the deadline applied to a client from the moment
	// we accept it until we finish the SOCKS5 handshake. 0 means use the
	// default.
	HandshakeTimeout time.Duration
}

const (
	// stopGrace is how long Stop waits for in-flight handlers to finish.
	stopGrace = 5 * time.Second

	// defaultHandshakeTimeout bounds the pre-relay handshake so a silent
	// client can't pin a goroutine forever.
	defaultHandshakeTimeout = 30 * time.Second
)

// dialFunc abstracts "open a connection to address through whatever
// transport the listener is wired to". Production wires it to chain.Dial;
// tests use a net.Pipe-backed stub.
type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// SOCKSv5 is a SOCKS5 TCP listener that routes each accepted connection
// through a single chain.
type SOCKSv5 struct {
	addr      string
	auth      *AuthCreds
	dial      dialFunc
	chainName string // for logging
	opts      Options

	// sem, if non-nil, is a buffered channel acting as a concurrency
	// semaphore: acquire before spawning a handler, release on return.
	sem chan struct{}

	mu     sync.Mutex
	ln     net.Listener
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewSOCKSv5 constructs a listener. ch must be non-nil; addr must be a TCP
// address understood by net.Listen. Pass a zero-valued Options for defaults.
func NewSOCKSv5(addr string, auth *AuthCreds, ch *chain.Chain, opts Options) *SOCKSv5 {
	var dial dialFunc
	name := ""
	if ch != nil {
		name = ch.Name
		dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			return ch.Dial(ctx, network, address)
		}
	}
	s := &SOCKSv5{addr: addr, auth: auth, dial: dial, chainName: name, opts: opts}
	if opts.MaxConnections > 0 {
		s.sem = make(chan struct{}, opts.MaxConnections)
	}
	return s
}

// handshakeTimeout returns the configured timeout or the default.
func (s *SOCKSv5) handshakeTimeout() time.Duration {
	if s.opts.HandshakeTimeout > 0 {
		return s.opts.HandshakeTimeout
	}
	return defaultHandshakeTimeout
}

// Protocol implements Listener.
func (s *SOCKSv5) Protocol() string { return "socks5" }

// Addr implements Listener. Returns the configured address before Start and
// the bound address afterwards.
func (s *SOCKSv5) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.addr
}

// Start implements Listener.
func (s *SOCKSv5) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ln != nil {
		return errors.New("socks5: already started")
	}
	if s.dial == nil {
		return errors.New("socks5: nil dialer")
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("socks5 listen %s: %w", s.addr, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	s.ln = ln
	s.cancel = cancel

	s.wg.Add(1)
	go s.acceptLoop(ctx, ln)

	log.Printf("socks5 listener started on %s (chain=%q)", ln.Addr(), s.chainName)
	return nil
}

// Stop implements Listener.
func (s *SOCKSv5) Stop() error {
	s.mu.Lock()
	if s.ln == nil {
		s.mu.Unlock()
		return nil
	}
	ln := s.ln
	cancel := s.cancel
	s.ln = nil
	s.cancel = nil
	s.mu.Unlock()

	cancel()
	closeErr := ln.Close()

	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(stopGrace):
		log.Printf("socks5: stop grace period expired; abandoning in-flight handlers")
	}

	if closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
		return fmt.Errorf("socks5 close: %w", closeErr)
	}
	return nil
}

func (s *SOCKSv5) acceptLoop(ctx context.Context, ln net.Listener) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			log.Printf("socks5 accept: %v", err)
			// Transient error (e.g. fd limit) — brief backoff avoids a hot loop.
			select {
			case <-time.After(100 * time.Millisecond):
			case <-ctx.Done():
				return
			}
			continue
		}

		// Acquire a slot from the semaphore before spawning the handler.
		// When the ceiling is hit, this blocks until another handler
		// finishes, applying natural back-pressure to new accepts.
		// During shutdown, ctx.Done() unblocks and we close the conn.
		if s.sem != nil {
			select {
			case s.sem <- struct{}{}:
			case <-ctx.Done():
				_ = conn.Close()
				return
			}
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			if s.sem != nil {
				defer func() { <-s.sem }()
			}
			s.handleConn(ctx, conn)
		}()
	}
}

func (s *SOCKSv5) handleConn(ctx context.Context, client net.Conn) {
	// Bound the handshake so a silent client can't pin a goroutine forever.
	// Once relaying starts we clear the deadline.
	_ = client.SetDeadline(time.Now().Add(s.handshakeTimeout()))

	if err := s.negotiate(client); err != nil {
		log.Printf("socks5: negotiation from %s failed: %v", client.RemoteAddr(), err)
		return
	}

	req, err := readRequest(client)
	if err != nil {
		log.Printf("socks5: request from %s failed: %v", client.RemoteAddr(), err)
		_ = writeReply(client, repGeneralFailure, "")
		return
	}

	switch req.cmd {
	case cmdConnect:
		s.handleConnect(ctx, client, req)
	case cmdUDPAssociate, cmdBind:
		_ = writeReply(client, repCmdNotSupported, "")
	default:
		_ = writeReply(client, repCmdNotSupported, "")
	}
}

// negotiate runs the method-selection and (optional) user/pass handshakes.
func (s *SOCKSv5) negotiate(client net.Conn) error {
	methods, err := readMethodSelection(client)
	if err != nil {
		return err
	}

	want := byte(methodNoAuth)
	if s.auth != nil {
		want = methodUserPass
	}
	if !containsByte(methods, want) {
		_ = writeMethodSelection(client, methodNone)
		return fmt.Errorf("client did not offer method %#x", want)
	}
	if err := writeMethodSelection(client, want); err != nil {
		return fmt.Errorf("write method selection: %w", err)
	}

	if s.auth == nil {
		return nil
	}

	user, pass, err := readUserPassAuth(client)
	if err != nil {
		return err
	}
	ok := subtle.ConstantTimeCompare([]byte(user), []byte(s.auth.Username)) == 1 &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(s.auth.Password)) == 1
	if err := writeUserPassReply(client, ok); err != nil {
		return fmt.Errorf("write auth reply: %w", err)
	}
	if !ok {
		return errors.New("auth rejected")
	}
	return nil
}

func (s *SOCKSv5) handleConnect(ctx context.Context, client net.Conn, req request) {
	dialCtx, cancelDial := context.WithTimeout(ctx, 30*time.Second)
	defer cancelDial()

	target := req.target()
	remote, err := s.dial(dialCtx, "tcp", target)
	if err != nil {
		log.Printf("socks5: chain dial %s failed: %v", target, err)
		_ = writeReply(client, replyCodeForDialErr(err), "")
		return
	}
	defer remote.Close()

	if err := writeReply(client, repSuccess, ""); err != nil {
		log.Printf("socks5: write success reply: %v", err)
		return
	}
	// Relay begins now — clear the handshake deadline.
	_ = client.SetDeadline(time.Time{})

	relay(client, remote)
}

// relay shuttles bytes between the SOCKS client and the proxy chain until
// either side closes. It closes the read-side of the other peer when one
// direction finishes so io.Copy returns promptly.
func relay(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		_ = closeWrite(a)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		_ = closeWrite(b)
	}()
	wg.Wait()
}

// closeWrite performs a half-close if the conn supports it, otherwise a
// full close. TCP connections (both net.TCPConn and tls.Conn) support it;
// the chain's protocol.Conn may or may not.
func closeWrite(c net.Conn) error {
	type writeCloser interface{ CloseWrite() error }
	if cw, ok := c.(writeCloser); ok {
		return cw.CloseWrite()
	}
	return c.Close()
}

func containsByte(b []byte, v byte) bool {
	for _, x := range b {
		if x == v {
			return true
		}
	}
	return false
}
