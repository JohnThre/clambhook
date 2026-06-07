package listener

import (
	"bufio"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/events"
)

// HTTPInspector is implemented by the opt-in developer-mode inspector. The
// listener owns only this narrow interface so normal traffic metadata does not
// grow request/response payload semantics.
type HTTPInspector interface {
	Enabled() bool
	MITMEnabled() bool
	TLSConfig(host string) (*tls.Config, error)
	Begin(context.Context, HTTPCaptureMeta, *http.Request) HTTPInspection
}

// HTTPCaptureMeta identifies a transaction for developer capture.
type HTTPCaptureMeta struct {
	ConnID     string
	Profile    string
	ClientAddr string
	ChainName  string
	Scheme     string
	Target     string
	StartedAt  time.Time
}

// HTTPInspection wraps request and response bodies for bounded capture.
type HTTPInspection interface {
	RequestBody(io.ReadCloser) io.ReadCloser
	ResponseBody(io.ReadCloser) io.ReadCloser
	Finish(*http.Response, error)
}

const (
	// maxRequestBytes caps the initial request (request-line + headers) to
	// prevent a malicious client from OOM-ing the daemon with unbounded
	// headers. net/http.Server applies a similar bound via MaxHeaderBytes.
	maxRequestBytes = 1 << 20 // 1 MiB

	// dialTimeout bounds each outbound dial through the chain.
	dialTimeout = 30 * time.Second
)

// HTTP is an HTTP proxy listener that serves both CONNECT tunnels and
// absolute-URI forward requests. Each accepted connection handles exactly
// one request and then closes — keep-alive is intentionally disabled so
// every request gets a fresh chain dial.
type HTTP struct {
	addr      string
	auth      *AuthCreds
	dial      dialFunc
	planner   RoutePlanner
	ch        *chain.Chain
	chainName string
	opts      Options
	sem       chan struct{}
	active    atomic.Int64
	mu        sync.Mutex
	ln        net.Listener
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// NewHTTPWithPlanner constructs an HTTP proxy whose target routing is decided
// per request.
func NewHTTPWithPlanner(addr string, auth *AuthCreds, planner RoutePlanner, opts Options) *HTTP {
	s := &HTTP{
		addr:    addr,
		auth:    auth,
		planner: planner,
		opts:    opts,
	}
	if planner != nil {
		s.chainName = planner.DefaultChainName()
	}
	if opts.MaxConnections > 0 {
		s.sem = make(chan struct{}, opts.MaxConnections)
	}
	return s
}

// NewHTTP constructs an HTTP listener. ch must be non-nil; addr must be a
// TCP address understood by net.Listen. Pass a zero-valued Options for
// defaults.
func NewHTTP(addr string, auth *AuthCreds, ch *chain.Chain, opts Options) *HTTP {
	var dial dialFunc
	name := ""
	if ch != nil {
		name = ch.Name
		dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			return ch.Dial(ctx, network, address)
		}
	}
	s := &HTTP{
		addr:      addr,
		auth:      auth,
		dial:      dial,
		ch:        ch,
		chainName: name,
		opts:      opts,
	}
	if opts.MaxConnections > 0 {
		s.sem = make(chan struct{}, opts.MaxConnections)
	}
	return s
}

// Protocol implements Listener.
func (s *HTTP) Protocol() string { return "http" }

// Addr implements Listener.
func (s *HTTP) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.addr
}

// ActiveConns implements Listener.
func (s *HTTP) ActiveConns() int64 { return s.active.Load() }

// handshakeTimeout returns the configured timeout or the default.
func (s *HTTP) handshakeTimeout() time.Duration {
	if s.opts.HandshakeTimeout > 0 {
		return s.opts.HandshakeTimeout
	}
	return defaultHandshakeTimeout
}

// Start implements Listener.
func (s *HTTP) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ln != nil {
		return errors.New("http: already started")
	}
	if s.dial == nil && s.planner == nil {
		return errors.New("http: nil dialer")
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("http listen %s: %w", s.addr, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	s.ln = ln
	s.cancel = cancel

	s.wg.Add(1)
	go s.acceptLoop(ctx, ln)

	log.Printf("http listener started on %s (chain=%q)", ln.Addr(), s.chainName)
	return nil
}

// Stop implements Listener.
func (s *HTTP) Stop() error {
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
		log.Printf("http: stop grace period expired; abandoning in-flight handlers")
	}

	if closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
		return fmt.Errorf("http close: %w", closeErr)
	}
	return nil
}

func (s *HTTP) acceptLoop(ctx context.Context, ln net.Listener) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			log.Printf("http accept: %v", err)
			select {
			case <-time.After(100 * time.Millisecond):
			case <-ctx.Done():
				return
			}
			continue
		}

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

			ce := newConnEvents(s.opts.EventBus, events.ListenerInfo{
				Protocol: s.Protocol(),
				Addr:     s.Addr(),
			}, s.opts.ProfileName, conn.RemoteAddr().String(), s.chainName)
			ce.emitOpened()

			relayErr := s.handleConn(ctx, conn, ce)
			ce.emitClosed(classifyClose(ctx, relayErr))
		}()
	}
}

// handleConn returns the relay/forward error (if any) so connection.closed
// can classify the reason.
func (s *HTTP) handleConn(ctx context.Context, client net.Conn, ce *connEvents) error {
	// Bound the initial request read so a silent client can't pin a goroutine.
	// Cleared inside each handler before relaying/streaming.
	_ = client.SetDeadline(time.Now().Add(s.handshakeTimeout()))

	// Size-bound the request-line + headers to prevent a malicious client
	// from consuming unbounded memory with an infinite header stream. The
	// cap is released once the request parses so the body / tunneled bytes
	// that follow are streamed without a ceiling.
	cr := &capReader{src: client, left: maxRequestBytes, capped: true}
	br := bufio.NewReader(cr)
	req, err := http.ReadRequest(br)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			// Don't log headers or their content — could contain credentials.
			log.Printf("http: read request from %s: %v", client.RemoteAddr(), err)
		}
		writeSimpleStatus(client, "HTTP/1.1", http.StatusBadRequest, "Bad Request")
		return nil
	}
	cr.capped = false

	if !s.checkProxyAuth(req.Header) {
		writeProxyAuthRequired(client, req.Proto)
		return nil
	}

	switch {
	case req.Method == http.MethodConnect:
		if s.opts.HTTPInspector != nil && s.opts.HTTPInspector.MITMEnabled() {
			return s.handleMITMConnect(ctx, client, br, req, ce)
		}
		return s.handleConnect(ctx, client, br, req, ce)
	case req.URL.IsAbs() && strings.EqualFold(req.URL.Scheme, "http"):
		return s.handleForward(ctx, client, req, ce)
	case req.URL.IsAbs() && strings.EqualFold(req.URL.Scheme, "https"):
		// Plain GET/POST to https://... would require MITM. Reject —
		// clients should use CONNECT for TLS targets.
		writeSimpleStatus(client, req.Proto, http.StatusBadRequest,
			"Bad Request: use CONNECT for HTTPS")
	default:
		writeSimpleStatus(client, req.Proto, http.StatusBadRequest,
			"Bad Request: absolute-URI required")
	}
	return nil
}

func (s *HTTP) handleConnect(ctx context.Context, client net.Conn, br *bufio.Reader, req *http.Request, ce *connEvents) error {
	if req.ContentLength > 0 || len(req.TransferEncoding) > 0 {
		writeSimpleStatus(client, req.Proto, http.StatusBadRequest,
			"Bad Request: body on CONNECT")
		return nil
	}
	if req.Host == "" {
		writeSimpleStatus(client, req.Proto, http.StatusBadRequest,
			"Bad Request: missing target")
		return nil
	}
	// Validate host:port form (also handles IPv6 brackets via SplitHostPort).
	if _, _, err := net.SplitHostPort(req.Host); err != nil {
		writeSimpleStatus(client, req.Proto, http.StatusBadRequest,
			"Bad Request: invalid target")
		return nil
	}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	dialCtx = ce.attach(dialCtx)
	plan, err := s.plan(dialCtx, "tcp", req.Host, client.RemoteAddr().String())
	if err != nil {
		log.Printf("http: route plan %s failed: %v", req.Host, err)
		writeSimpleStatus(client, req.Proto, http.StatusBadGateway, "Bad Gateway")
		return err
	}
	plan.Visibility = httpConnectVisibility(req.Host)
	ce.emitRuleDecision(plan)
	ce.emitDialingPlan(plan)
	if plan.Action == RouteActionBlock || plan.Action == RouteActionReject {
		writeSimpleStatus(client, req.Proto, http.StatusForbidden, "Forbidden")
		if plan.Action == RouteActionReject {
			return ErrRouteRejected
		}
		return ErrRouteBlocked
	}

	remote, err := plan.Dial(dialCtx, "tcp", req.Host)
	if err != nil {
		log.Printf("http: CONNECT chain dial %s failed: %v", req.Host, err)
		code, reason := httpStatusForDialErr(err)
		writeSimpleStatus(client, req.Proto, code, reason)
		return err
	}
	defer remote.Close()

	if _, err := io.WriteString(client,
		"HTTP/1.1 200 Connection established\r\nProxy-Agent: clambhook\r\n\r\n"); err != nil {
		log.Printf("http: write CONNECT 200 to %s: %v", client.RemoteAddr(), err)
		return err
	}

	ce.emitEstablished()

	// Clear the handshake deadline — long-lived tunnels must not time out.
	_ = client.SetDeadline(time.Time{})

	// Watchdog: if the parent context cancels (daemon shutting down), close
	// both conns so the relay unblocks within stopGrace.
	stopCh := make(chan struct{})
	defer close(stopCh)
	go func() {
		select {
		case <-ctx.Done():
			_ = remote.Close()
			_ = client.Close()
		case <-stopCh:
		}
	}()

	// Wrap the client so reads go through the bufio.Reader. This preserves
	// any early-data bytes the client pipelined after the CONNECT request
	// (e.g. a TLS ClientHello sent in the same syscall as CONNECT). A raw
	// net.Conn would have already consumed those into the bufio buffer.
	shim := &bufReadConn{Conn: client, br: br}
	return relay(shim, remote, ce.rxCounter(), ce.txCounter())
}

func (s *HTTP) handleMITMConnect(ctx context.Context, client net.Conn, br *bufio.Reader, req *http.Request, ce *connEvents) error {
	if req.ContentLength > 0 || len(req.TransferEncoding) > 0 {
		writeSimpleStatus(client, req.Proto, http.StatusBadRequest,
			"Bad Request: body on CONNECT")
		return nil
	}
	if req.Host == "" {
		writeSimpleStatus(client, req.Proto, http.StatusBadRequest,
			"Bad Request: missing target")
		return nil
	}
	host, _, err := net.SplitHostPort(req.Host)
	if err != nil {
		writeSimpleStatus(client, req.Proto, http.StatusBadRequest,
			"Bad Request: invalid target")
		return nil
	}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	dialCtx = ce.attach(dialCtx)
	plan, err := s.plan(dialCtx, "tcp", req.Host, client.RemoteAddr().String())
	if err != nil {
		log.Printf("http: MITM route plan %s failed: %v", req.Host, err)
		writeSimpleStatus(client, req.Proto, http.StatusBadGateway, "Bad Gateway")
		return err
	}
	plan.Visibility = httpConnectVisibility(req.Host)
	ce.emitRuleDecision(plan)
	ce.emitDialingPlan(plan)
	if plan.Action == RouteActionBlock || plan.Action == RouteActionReject {
		writeSimpleStatus(client, req.Proto, http.StatusForbidden, "Forbidden")
		if plan.Action == RouteActionReject {
			return ErrRouteRejected
		}
		return ErrRouteBlocked
	}

	remote, err := plan.Dial(dialCtx, "tcp", req.Host)
	if err != nil {
		log.Printf("http: MITM chain dial %s failed: %v", req.Host, err)
		code, reason := httpStatusForDialErr(err)
		writeSimpleStatus(client, req.Proto, code, reason)
		return err
	}
	defer remote.Close()

	tlsConfig, err := s.opts.HTTPInspector.TLSConfig(host)
	if err != nil {
		log.Printf("http: MITM cert %s failed: %v", host, err)
		writeSimpleStatus(client, req.Proto, http.StatusBadGateway, "Bad Gateway")
		return err
	}

	if _, err := io.WriteString(client,
		"HTTP/1.1 200 Connection established\r\nProxy-Agent: clambhook\r\n\r\n"); err != nil {
		log.Printf("http: write MITM CONNECT 200 to %s: %v", client.RemoteAddr(), err)
		return err
	}
	ce.emitEstablished()
	_ = client.SetDeadline(time.Time{})

	stopCh := make(chan struct{})
	defer close(stopCh)
	go func() {
		select {
		case <-ctx.Done():
			_ = remote.Close()
			_ = client.Close()
		case <-stopCh:
		}
	}()

	clientTLS := tls.Server(&bufReadConn{Conn: client, br: br}, tlsConfig)
	if err := clientTLS.HandshakeContext(ctx); err != nil {
		log.Printf("http: MITM client TLS handshake %s failed: %v", client.RemoteAddr(), err)
		return err
	}
	defer clientTLS.Close()

	serverName := strings.Trim(host, "[]")
	remoteTLS := tls.Client(remote, &tls.Config{
		ServerName: serverName,
		NextProtos: []string{"http/1.1"},
		MinVersion: tls.VersionTLS12,
	})
	if err := remoteTLS.HandshakeContext(ctx); err != nil {
		log.Printf("http: MITM origin TLS handshake %s failed: %v", req.Host, err)
		return err
	}
	defer remoteTLS.Close()

	clientReader := bufio.NewReader(clientTLS)
	remoteConn := net.Conn(remoteTLS)
	if ce != nil {
		remoteConn = &meteredConn{Conn: remoteTLS, rxCounter: ce.rxCounter(), txCounter: ce.txCounter()}
	}
	remoteReader := bufio.NewReader(remoteConn)
	for {
		mitmReq, err := http.ReadRequest(clientReader)
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		if err != nil {
			log.Printf("http: MITM read request from %s: %v", client.RemoteAddr(), err)
			return err
		}
		if err := s.forwardMITMRequest(ctx, clientTLS, remoteConn, remoteReader, mitmReq, req.Host, client.RemoteAddr().String(), ce); err != nil {
			return err
		}
		if mitmReq.Close {
			return nil
		}
	}
}

func (s *HTTP) forwardMITMRequest(ctx context.Context, client io.Writer, remote io.Writer, remoteReader *bufio.Reader, req *http.Request, target, clientAddr string, ce *connEvents) error {
	inspection := s.beginInspection(ctx, ce, clientAddr, req, "https", target)
	if inspection != nil && req.Body != nil {
		req.Body = inspection.RequestBody(req.Body)
	}
	stripHopByHopHeaders(req.Header)
	req.RequestURI = ""
	req.URL.Scheme = ""
	req.URL.Host = ""

	if err := req.Write(remote); err != nil {
		log.Printf("http: MITM write request to %s: %v", target, err)
		if inspection != nil {
			inspection.Finish(nil, err)
		}
		return err
	}
	resp, err := http.ReadResponse(remoteReader, req)
	if err != nil {
		log.Printf("http: MITM read response from %s: %v", target, err)
		if inspection != nil {
			inspection.Finish(nil, err)
		}
		return err
	}
	defer resp.Body.Close()
	if inspection != nil && resp.Body != nil {
		resp.Body = inspection.ResponseBody(resp.Body)
	}
	stripHopByHopHeaders(resp.Header)
	if err := resp.Write(client); err != nil {
		log.Printf("http: MITM write response to client: %v", err)
		if inspection != nil {
			inspection.Finish(resp, err)
		}
		return err
	}
	if inspection != nil {
		inspection.Finish(resp, nil)
	}
	return nil
}

func (s *HTTP) handleForward(ctx context.Context, client net.Conn, req *http.Request, ce *connEvents) error {
	// Resolve target host:port — default port 80 for http://.
	host := req.URL.Hostname()
	port := req.URL.Port()
	if host == "" {
		writeSimpleStatus(client, req.Proto, http.StatusBadRequest,
			"Bad Request: missing host")
		return nil
	}
	if port == "" {
		port = "80"
	}
	target := net.JoinHostPort(host, port)

	dialCtx, dialCancel := context.WithTimeout(ctx, dialTimeout)
	dialCtx = ce.attach(dialCtx)
	plan, err := s.plan(dialCtx, "tcp", target, client.RemoteAddr().String())
	if err != nil {
		dialCancel()
		log.Printf("http: route plan %s failed: %v", target, err)
		writeSimpleStatus(client, req.Proto, http.StatusBadGateway, "Bad Gateway")
		return err
	}
	plan.Visibility = httpForwardVisibility(req)
	ce.emitRuleDecision(plan)
	ce.emitDialingPlan(plan)
	if plan.Action == RouteActionBlock || plan.Action == RouteActionReject {
		dialCancel()
		writeSimpleStatus(client, req.Proto, http.StatusForbidden, "Forbidden")
		if plan.Action == RouteActionReject {
			return ErrRouteRejected
		}
		return ErrRouteBlocked
	}

	remote, err := plan.Dial(dialCtx, "tcp", target)
	dialCancel()
	if err != nil {
		log.Printf("http: forward chain dial %s failed: %v", target, err)
		code, reason := httpStatusForDialErr(err)
		writeSimpleStatus(client, req.Proto, code, reason)
		return err
	}
	defer remote.Close()
	ce.emitEstablished()

	// Watchdog: close both sides on ctx cancel so resp.Write / req.Write /
	// body reads all unblock within stopGrace.
	stopCh := make(chan struct{})
	defer close(stopCh)
	go func() {
		select {
		case <-ctx.Done():
			_ = remote.Close()
			_ = client.Close()
		case <-stopCh:
		}
	}()

	// Clear the handshake deadline — large bodies must not time out.
	_ = client.SetDeadline(time.Time{})

	inspection := s.beginInspection(ctx, ce, client.RemoteAddr().String(), req, "http", target)
	if inspection != nil && req.Body != nil {
		req.Body = inspection.RequestBody(req.Body)
	}

	// Rewrite to origin-form and strip hop-by-hop headers before forwarding.
	stripHopByHopHeaders(req.Header)
	req.RequestURI = ""     // required by (*Request).Write
	req.Host = req.URL.Host // per RFC 7230 §5.4 for absolute-form
	req.URL.Scheme = ""     // origin form on the wire
	req.URL.Host = ""

	if ce != nil {
		// Count bytes flowing in each direction. For forward proxying we
		// can't easily instrument the stdlib req/resp write path, so we
		// count at the request-write / response-write boundary: bytes
		// written to remote are "tx", bytes read from remote (via the
		// ReadResponse bufio) are "rx". We approximate by wrapping the
		// remote conn's reads/writes. req.Write/resp.Write don't support
		// injecting a wrapper, so forward-proxy meters fall back to an
		// approximation at the boundary. (CONNECT tunnels use the precise
		// relay path.)
		remote = &meteredConn{Conn: remote, rxCounter: ce.rxCounter(), txCounter: ce.txCounter()}
	}

	if err := req.Write(remote); err != nil {
		log.Printf("http: forward write request to %s: %v", target, err)
		if inspection != nil {
			inspection.Finish(nil, err)
		}
		// Nothing sensible to say to the client — close.
		return err
	}

	resp, err := http.ReadResponse(bufio.NewReader(remote), req)
	if err != nil {
		log.Printf("http: forward read response from %s: %v", target, err)
		if inspection != nil {
			inspection.Finish(nil, err)
		}
		writeSimpleStatus(client, req.Proto, http.StatusBadGateway, "Bad Gateway")
		return err
	}
	defer resp.Body.Close()
	if inspection != nil && resp.Body != nil {
		resp.Body = inspection.ResponseBody(resp.Body)
	}

	stripHopByHopHeaders(resp.Header)

	if err := resp.Write(client); err != nil {
		// Client likely hung up mid-response — log and move on.
		log.Printf("http: forward write response to %s: %v", client.RemoteAddr(), err)
		if inspection != nil {
			inspection.Finish(resp, err)
		}
		return err
	}
	if inspection != nil {
		inspection.Finish(resp, nil)
	}
	return nil
}

func (s *HTTP) beginInspection(ctx context.Context, ce *connEvents, clientAddr string, req *http.Request, scheme, target string) HTTPInspection {
	if s.opts.HTTPInspector == nil || !s.opts.HTTPInspector.Enabled() {
		return nil
	}
	return s.opts.HTTPInspector.Begin(ctx, HTTPCaptureMeta{
		ConnID:     ce.id(),
		Profile:    s.opts.ProfileName,
		ClientAddr: clientAddr,
		ChainName:  s.chainName,
		Scheme:     scheme,
		Target:     target,
		StartedAt:  time.Now(),
	}, req)
}

func (s *HTTP) plan(ctx context.Context, network, target, source string) (RoutePlan, error) {
	if s.planner != nil {
		return PlanRoute(ctx, s.planner, network, target, source)
	}
	plan := RoutePlan{
		Profile:   s.opts.ProfileName,
		Action:    RouteActionChain,
		ChainName: s.chainName,
		Target:    target,
		Network:   network,
		Source:    source,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return s.dial(ctx, network, address)
		},
	}
	plan.Host, plan.Port = splitTrafficTarget(target)
	if s.ch != nil {
		plan.Hops = s.ch.HopInfo()
	}
	return plan, nil
}

func httpConnectVisibility(target string) events.VisibilityInfo {
	host, port := splitTrafficTarget(target)
	return events.VisibilityInfo{
		Kind:   "http_connect",
		Method: http.MethodConnect,
		Scheme: "https",
		Host:   host,
		Port:   port,
	}
}

func httpForwardVisibility(req *http.Request) events.VisibilityInfo {
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	return events.VisibilityInfo{
		Kind:   "http",
		Method: req.Method,
		Scheme: req.URL.Scheme,
		Host:   req.URL.Hostname(),
		Port:   req.URL.Port(),
		Path:   path,
	}
}

// meteredConn wraps a net.Conn to count bytes in both directions. Used on
// the forward-proxy path where req.Write / http.ReadResponse can't accept
// a plain io.Reader/Writer wrapper.
type meteredConn struct {
	net.Conn
	rxCounter *atomic.Uint64 // bytes from remote (Read)
	txCounter *atomic.Uint64 // bytes to remote (Write)
}

func (m *meteredConn) Read(p []byte) (int, error) {
	n, err := m.Conn.Read(p)
	if n > 0 && m.rxCounter != nil {
		m.rxCounter.Add(uint64(n))
	}
	return n, err
}

func (m *meteredConn) Write(p []byte) (int, error) {
	n, err := m.Conn.Write(p)
	if n > 0 && m.txCounter != nil {
		m.txCounter.Add(uint64(n))
	}
	return n, err
}

// checkProxyAuth verifies the Proxy-Authorization header against s.auth.
// Returns true when auth is disabled or credentials match. Never logs the
// header value — it contains base64-encoded credentials.
func (s *HTTP) checkProxyAuth(h http.Header) bool {
	if s.auth == nil {
		return true
	}
	raw := h.Get("Proxy-Authorization")
	if raw == "" {
		return false
	}
	const prefix = "Basic "
	if len(raw) <= len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(raw[len(prefix):])
	if err != nil {
		return false
	}
	sep := -1
	for i, c := range decoded {
		if c == ':' {
			sep = i
			break
		}
	}
	if sep < 0 {
		return false
	}
	user := decoded[:sep]
	pass := decoded[sep+1:]
	// Constant-time comparison to avoid credential-length timing leaks.
	userOK := subtle.ConstantTimeCompare(user, []byte(s.auth.Username)) == 1
	passOK := subtle.ConstantTimeCompare(pass, []byte(s.auth.Password)) == 1
	return userOK && passOK
}

// writeProxyAuthRequired emits 407 with a RFC 7617-compliant challenge. The
// charset="UTF-8" hint lets modern clients send UTF-8 credentials safely.
func writeProxyAuthRequired(w io.Writer, proto string) {
	if proto == "" {
		proto = "HTTP/1.1"
	}
	fmt.Fprintf(w,
		"%s 407 Proxy Authentication Required\r\n"+
			"Proxy-Authenticate: Basic realm=\"clambhook\", charset=\"UTF-8\"\r\n"+
			"Content-Length: 0\r\n"+
			"Connection: close\r\n\r\n",
		proto)
}

// writeSimpleStatus writes a minimal HTTP response with no body. Used for
// error replies and 200 OK fastpaths. Mirrors the client's protocol version
// so HTTP/1.0 callers aren't surprised by an HTTP/1.1 reply.
func writeSimpleStatus(w io.Writer, proto string, code int, reason string) {
	if proto == "" {
		proto = "HTTP/1.1"
	}
	fmt.Fprintf(w,
		"%s %d %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
		proto, code, reason)
}

// httpStatusForDialErr maps an outbound-dial error to a proxy status code.
// Detail goes to the log; the wire reason is deliberately generic so we
// don't leak chain hostnames or protocol errors to the client.
func httpStatusForDialErr(err error) (int, string) {
	if err == nil {
		return http.StatusOK, "OK"
	}
	if errors.Is(err, ErrRouteBlocked) || errors.Is(err, ErrRouteRejected) {
		return http.StatusForbidden, "Forbidden"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "Gateway Timeout"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return http.StatusGatewayTimeout, "Gateway Timeout"
	}
	return http.StatusBadGateway, "Bad Gateway"
}

// stripHopByHopHeaders removes headers that must not cross proxy hops, per
// RFC 7230 §6.1, plus the two Proxy-* headers that are strictly between
// client and proxy. The Connection header's token list names additional
// headers that are also hop-by-hop.
func stripHopByHopHeaders(h http.Header) {
	// Connection may list additional hop-by-hop headers — consume first.
	if conn := h.Get("Connection"); conn != "" {
		for _, tok := range strings.Split(conn, ",") {
			name := strings.TrimSpace(tok)
			if name != "" {
				h.Del(name)
			}
		}
	}
	for _, name := range hopByHopHeaders {
		h.Del(name)
	}
}

var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Proxy-Connection",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// bufReadConn preserves bufio.Reader-buffered bytes across a CONNECT
// handshake. After http.ReadRequest finishes, the bufio may hold bytes the
// client pipelined after the request line — those must be relayed to the
// remote, not discarded. Wrapping the client so subsequent reads go through
// the bufio achieves that without a separate pre-flush step.
//
// The embedded net.Conn interface doesn't promote CloseWrite(), so we
// forward it explicitly — the relay helper type-asserts for it to perform
// a half-close when one direction finishes.
type bufReadConn struct {
	net.Conn
	br *bufio.Reader
}

func (b *bufReadConn) Read(p []byte) (int, error) { return b.br.Read(p) }

func (b *bufReadConn) CloseWrite() error {
	type writeCloser interface{ CloseWrite() error }
	if cw, ok := b.Conn.(writeCloser); ok {
		return cw.CloseWrite()
	}
	return b.Conn.Close()
}

// capReader limits reads to `left` bytes while `capped` is true, then
// unbounded once released. Used to bound the header-read phase without
// truncating the body or a CONNECT tunnel that follows.
type capReader struct {
	src    io.Reader
	left   int64
	capped bool
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.capped {
		if c.left <= 0 {
			return 0, errHeaderTooLarge
		}
		if int64(len(p)) > c.left {
			p = p[:c.left]
		}
	}
	n, err := c.src.Read(p)
	if c.capped {
		c.left -= int64(n)
	}
	return n, err
}

var errHeaderTooLarge = errors.New("http: request headers exceed limit")
