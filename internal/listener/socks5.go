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
	"sync/atomic"
	"time"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/events"
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

	// EventBus, when non-nil, receives per-connection lifecycle and
	// bandwidth events. nil disables emission entirely — no allocations,
	// no atomic adds — so tests that don't exercise events pay zero cost.
	EventBus *events.Bus
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

// packetDialFunc abstracts "open a UDP-carrying session through the chain".
// Production wires it to chain.DialPacket; tests stub it out. nil means the
// listener's chain does not support UDP — UDP ASSOCIATE requests will be
// rejected with reply 0x07 (command not supported).
type packetDialFunc func(ctx context.Context, address string) (net.PacketConn, error)

// SOCKSv5 is a SOCKS5 TCP listener that routes each accepted connection
// through a single chain.
type SOCKSv5 struct {
	addr       string
	auth       *AuthCreds
	dial       dialFunc
	dialPacket packetDialFunc // optional — nil means UDP ASSOCIATE is rejected
	planner    RoutePlanner
	ch         *chain.Chain // kept for HopInfo emission; nil disables hop events
	chainName  string       // for logging
	opts       Options

	// sem, if non-nil, is a buffered channel acting as a concurrency
	// semaphore: acquire before spawning a handler, release on return.
	sem chan struct{}

	// active tracks in-flight handlers for observability. Incremented once
	// the goroutine is actually running, decremented on return.
	active atomic.Int64

	mu     sync.Mutex
	ln     net.Listener
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewSOCKSv5WithPlanner constructs a listener whose target routing is decided
// per connection instead of being fixed to one chain.
func NewSOCKSv5WithPlanner(addr string, auth *AuthCreds, planner RoutePlanner, opts Options) *SOCKSv5 {
	s := &SOCKSv5{
		addr:      addr,
		auth:      auth,
		planner:   planner,
		chainName: "",
		opts:      opts,
	}
	if planner != nil {
		s.chainName = planner.DefaultChainName()
	}
	if opts.MaxConnections > 0 {
		s.sem = make(chan struct{}, opts.MaxConnections)
	}
	return s
}

// ActiveConns implements Listener.
func (s *SOCKSv5) ActiveConns() int64 { return s.active.Load() }

// NewSOCKSv5 constructs a listener. ch must be non-nil; addr must be a TCP
// address understood by net.Listen. Pass a zero-valued Options for defaults.
//
// The returned listener advertises UDP ASSOCIATE support by probing ch for
// a PacketDialer at Start time. If any hop in the chain can't carry UDP,
// UDP requests fall back to reply 0x07 (command not supported).
func NewSOCKSv5(addr string, auth *AuthCreds, ch *chain.Chain, opts Options) *SOCKSv5 {
	var dial dialFunc
	var dialPacket packetDialFunc
	name := ""
	if ch != nil {
		name = ch.Name
		dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			return ch.Dial(ctx, network, address)
		}
		dialPacket = func(ctx context.Context, address string) (net.PacketConn, error) {
			pc, err := ch.DialPacket(ctx, address)
			if err != nil {
				return nil, err
			}
			return pc, nil
		}
	}
	s := &SOCKSv5{
		addr:       addr,
		auth:       auth,
		dial:       dial,
		dialPacket: dialPacket,
		ch:         ch,
		chainName:  name,
		opts:       opts,
	}
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
	if s.dial == nil && s.planner == nil {
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
			s.active.Add(1)
			defer s.active.Add(-1)

			// Allocate per-connection event plumbing. nil when events are
			// disabled (Options.EventBus unset); all connEvents methods
			// below are nil-safe.
			ce := newConnEvents(s.opts.EventBus, events.ListenerInfo{
				Protocol: s.Protocol(),
				Addr:     s.Addr(),
			}, conn.RemoteAddr().String(), s.chainName)
			ce.emitOpened()

			relayErr := s.handleConn(ctx, conn, ce)
			ce.emitClosed(classifyClose(ctx, relayErr))
		}()
	}
}

// handleConn returns the relay error (if any) so the deferred
// connection.closed event can classify the close reason correctly. A nil
// return covers both "handshake failed before relay started" (where reason
// is determined by ctx) and "relay finished cleanly".
func (s *SOCKSv5) handleConn(ctx context.Context, client net.Conn, ce *connEvents) error {
	// Bound the handshake so a silent client can't pin a goroutine forever.
	// Once relaying starts we clear the deadline.
	_ = client.SetDeadline(time.Now().Add(s.handshakeTimeout()))

	if err := s.negotiate(client); err != nil {
		log.Printf("socks5: negotiation from %s failed: %v", client.RemoteAddr(), err)
		return nil
	}

	req, err := readRequest(client)
	if err != nil {
		log.Printf("socks5: request from %s failed: %v", client.RemoteAddr(), err)
		_ = writeReply(client, repGeneralFailure, "")
		return nil
	}

	switch req.cmd {
	case cmdConnect:
		return s.handleConnect(ctx, client, req, ce)
	case cmdUDPAssociate:
		if s.dialPacket == nil {
			_ = writeReply(client, repCmdNotSupported, "")
			return nil
		}
		s.handleUDPAssociate(ctx, client, ce)
		return nil
	case cmdBind:
		_ = writeReply(client, repCmdNotSupported, "")
	default:
		_ = writeReply(client, repCmdNotSupported, "")
	}
	return nil
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

func (s *SOCKSv5) handleConnect(ctx context.Context, client net.Conn, req request, ce *connEvents) error {
	dialCtx, cancelDial := context.WithTimeout(ctx, 30*time.Second)
	defer cancelDial()

	// Attach event plumbing so chain.Dial can emit per-hop events.
	dialCtx = ce.attach(dialCtx)

	target := req.target()
	plan, err := s.plan(dialCtx, "tcp", target)
	if err != nil {
		log.Printf("socks5: route plan %s failed: %v", target, err)
		_ = writeReply(client, repGeneralFailure, "")
		return err
	}
	ce.emitRuleDecision(plan)
	ce.emitDialingPlan(plan)
	if plan.Action == RouteActionBlock || plan.Action == RouteActionReject {
		_ = writeReply(client, repConnNotAllowed, "")
		return ErrRouteBlocked
	}

	remote, err := plan.Dial(dialCtx, "tcp", target)
	if err != nil {
		log.Printf("socks5: chain dial %s failed: %v", target, err)
		_ = writeReply(client, replyCodeForDialErr(err), "")
		return err
	}
	defer remote.Close()

	if err := writeReply(client, repSuccess, ""); err != nil {
		log.Printf("socks5: write success reply: %v", err)
		return err
	}
	// Relay begins now — clear the handshake deadline.
	_ = client.SetDeadline(time.Time{})

	ce.emitEstablished()

	return relay(client, remote, ce.rxCounter(), ce.txCounter())
}

func (s *SOCKSv5) plan(ctx context.Context, network, target string) (RoutePlan, error) {
	if s.planner != nil {
		return s.planner.Plan(ctx, network, target)
	}
	plan := RoutePlan{
		Action:    RouteActionChain,
		ChainName: s.chainName,
		Target:    target,
		Network:   network,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return s.dial(ctx, network, address)
		},
	}
	plan.Host, plan.Port = splitTrafficTarget(target)
	if s.ch != nil {
		plan.Hops = s.ch.HopInfo()
	}
	if s.dialPacket != nil {
		plan.DialPacket = func(ctx context.Context, address string) (net.PacketConn, error) {
			return s.dialPacket(ctx, address)
		}
	}
	return plan, nil
}

// relay shuttles bytes between the SOCKS client `a` and the proxy-chain
// endpoint `b` until either side closes. It closes the read-side of the
// other peer when one direction finishes so io.Copy returns promptly.
//
// rxCounter and txCounter are optional byte meters. rxCounter accumulates
// bytes read FROM b (flowing to a) — "incoming" from the client's
// perspective. txCounter accumulates bytes read FROM a (flowing to b) —
// "outgoing". Passing nil for either counter disables metering for that
// direction.
//
// Returns the first non-EOF / non-ErrClosed error observed on either copy
// direction. Normal termination returns nil — which the caller uses to
// classify the connection.closed reason.
func relay(a, b net.Conn, rxCounter, txCounter *atomic.Uint64) error {
	var wg sync.WaitGroup
	var rxErr, txErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		src := io.Reader(b)
		if rxCounter != nil {
			src = events.NewMeteredReader(b, rxCounter)
		}
		_, err := io.Copy(a, src)
		rxErr = normalizeRelayErr(err)
		_ = closeWrite(a)
	}()
	go func() {
		defer wg.Done()
		src := io.Reader(a)
		if txCounter != nil {
			src = events.NewMeteredReader(a, txCounter)
		}
		_, err := io.Copy(b, src)
		txErr = normalizeRelayErr(err)
		_ = closeWrite(b)
	}()
	wg.Wait()

	if rxErr != nil {
		return rxErr
	}
	return txErr
}

// normalizeRelayErr filters out the "expected" end-of-stream signals that
// a proxy relay sees on every healthy close so they don't pollute the
// close-reason classification. EOF and net.ErrClosed both mean "the peer
// hung up cleanly" in this context.
func normalizeRelayErr(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
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
