package dnsproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/listener"
	"github.com/quic-go/quic-go"
)

const (
	defaultTimeout = 5 * time.Second
	maxDNSMessage  = 65535

	protocolDoH = "doh"
	protocolDoT = "dot"
	protocolDoQ = "doq"
)

var configureTLSForTest func(*tls.Config)

// Proxy forwards DNS wire queries to encrypted upstream resolvers.
type Proxy struct {
	timeout   time.Duration
	upstreams []upstream
}

type upstream interface {
	Name() string
	Exchange(context.Context, []byte) ([]byte, error)
	Close() error
}

// New builds a DNS proxy from profile config. A disabled config returns nil.
func New(cfg config.DNSConfig, planner listener.RoutePlanner) (*Proxy, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if planner == nil {
		return nil, errors.New("dns: nil route planner")
	}
	timeout := cfg.Timeout.Std()
	if timeout == 0 {
		timeout = defaultTimeout
	}
	p := &Proxy{timeout: timeout}
	for i, raw := range cfg.Upstreams {
		up, err := newUpstream(raw, planner)
		if err != nil {
			p.Close()
			return nil, fmt.Errorf("dns upstream %d: %w", i, err)
		}
		p.upstreams = append(p.upstreams, up)
	}
	if len(p.upstreams) == 0 {
		return nil, errors.New("dns: no upstreams configured")
	}
	return p, nil
}

func newUpstream(cfg config.DNSUpstreamConfig, planner listener.RoutePlanner) (upstream, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Protocol)) {
	case protocolDoH:
		return newDoHUpstream(cfg, planner)
	case protocolDoT:
		return newDoTUpstream(cfg, planner)
	case protocolDoQ:
		return newDoQUpstream(cfg, planner)
	default:
		return nil, fmt.Errorf("unknown protocol %q", cfg.Protocol)
	}
}

// Exchange returns a DNS wire response. If all upstreams fail and the query is
// parseable enough to identify the question, it returns a SERVFAIL response
// alongside the upstream error so callers can both answer the client and log.
func (p *Proxy) Exchange(ctx context.Context, query []byte) ([]byte, error) {
	if p == nil {
		return nil, errors.New("dns: proxy is nil")
	}
	if !validQuery(query) {
		return nil, errors.New("dns: malformed query")
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	var lastErr error
	for _, up := range p.upstreams {
		resp, err := up.Exchange(ctx, query)
		if err == nil {
			return resp, nil
		}
		lastErr = fmt.Errorf("%s: %w", up.Name(), err)
	}
	return servfail(query), lastErr
}

// Close releases persistent upstream state.
func (p *Proxy) Close() error {
	if p == nil {
		return nil
	}
	var errs []error
	for _, up := range p.upstreams {
		if err := up.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type routedEndpoint struct {
	planner      listener.RoutePlanner
	target       string
	host         string
	port         string
	bootstrapIPs []netip.Addr
}

func newRoutedEndpoint(planner listener.RoutePlanner, target string, bootstrap []string) (routedEndpoint, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return routedEndpoint{}, err
	}
	out := routedEndpoint{
		planner: planner,
		target:  target,
		host:    strings.Trim(host, "[]"),
		port:    port,
	}
	for _, raw := range bootstrap {
		ip, err := netip.ParseAddr(raw)
		if err != nil {
			return routedEndpoint{}, fmt.Errorf("bootstrap %q: %w", raw, err)
		}
		out.bootstrapIPs = append(out.bootstrapIPs, ip)
	}
	return out, nil
}

func (e routedEndpoint) validateNoLocalResolve(network string) error {
	if net.ParseIP(e.host) != nil || len(e.bootstrapIPs) > 0 {
		return nil
	}
	plan, err := e.planner.Plan(context.Background(), network, e.target)
	if err != nil {
		return err
	}
	if plan.Action == listener.RouteActionDirect {
		return fmt.Errorf("direct DNS upstream %q needs bootstrap_ips to avoid local DNS resolution", e.target)
	}
	return nil
}

func (e routedEndpoint) dialTCP(ctx context.Context, target string) (net.Conn, error) {
	plan, err := e.planner.Plan(ctx, "tcp", target)
	if err != nil {
		return nil, err
	}
	if plan.Action == listener.RouteActionBlock || plan.Action == listener.RouteActionReject {
		return nil, fmt.Errorf("route %s for %s", plan.Action, target)
	}
	if plan.Dial == nil {
		return nil, fmt.Errorf("route %s has no TCP dialer", target)
	}
	addresses, err := e.addressesForPlan(plan, target)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, address := range addresses {
		conn, err := plan.Dial(ctx, "tcp", address)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (e routedEndpoint) dialPacket(ctx context.Context) (net.PacketConn, net.Addr, error) {
	plan, err := e.planner.Plan(ctx, "udp", e.target)
	if err != nil {
		return nil, nil, err
	}
	if plan.Action == listener.RouteActionBlock || plan.Action == listener.RouteActionReject {
		return nil, nil, fmt.Errorf("route %s for %s", plan.Action, e.target)
	}
	if plan.DialPacket == nil {
		return nil, nil, fmt.Errorf("route %s has no UDP dialer", e.target)
	}
	addresses, err := e.addressesForPlan(plan, e.target)
	if err != nil {
		return nil, nil, err
	}
	var lastErr error
	for _, address := range addresses {
		pc, err := plan.DialPacket(ctx, address)
		if err != nil {
			lastErr = err
			continue
		}
		addr, err := packetAddrForPlan(plan, address)
		if err != nil {
			_ = pc.Close()
			lastErr = err
			continue
		}
		return pc, addr, nil
	}
	return nil, nil, lastErr
}

func (e routedEndpoint) addressesForPlan(plan listener.RoutePlan, target string) ([]string, error) {
	if plan.Action != listener.RouteActionDirect {
		return []string{target}, nil
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		return []string{net.JoinHostPort(host, port)}, nil
	}
	if len(e.bootstrapIPs) == 0 {
		return nil, fmt.Errorf("direct DNS upstream %q needs bootstrap_ips to avoid local DNS resolution", target)
	}
	out := make([]string, 0, len(e.bootstrapIPs))
	for _, ip := range e.bootstrapIPs {
		out = append(out, net.JoinHostPort(ip.String(), port))
	}
	return out, nil
}

func packetAddrForPlan(plan listener.RoutePlan, target string) (net.Addr, error) {
	if plan.Action != listener.RouteActionDirect {
		return stringAddr{network: "udp", address: target}, nil
	}
	addr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, err
	}
	return addr, nil
}

type stringAddr struct {
	network string
	address string
}

func (a stringAddr) Network() string { return a.network }
func (a stringAddr) String() string  { return a.address }

func upstreamName(name, fallback string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	return fallback
}

func serverNameFor(host, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return configured
	}
	host = strings.Trim(host, "[]")
	if net.ParseIP(host) != nil {
		return ""
	}
	return host
}

func newTLSConfig(minVersion uint16, serverName string, nextProtos ...string) *tls.Config {
	cfg := &tls.Config{
		MinVersion: minVersion,
		ServerName: serverName,
		NextProtos: nextProtos,
	}
	if configureTLSForTest != nil {
		configureTLSForTest(cfg)
	}
	return cfg
}

func validQuery(query []byte) bool {
	if len(query) < 12 {
		return false
	}
	return questionEnd(query) > 0
}

func dnsID(msg []byte) [2]byte {
	return [2]byte{msg[0], msg[1]}
}

func setDNSID(msg []byte, id [2]byte) {
	msg[0], msg[1] = id[0], id[1]
}

func questionEnd(msg []byte) int {
	if len(msg) < 12 {
		return 0
	}
	qdCount := int(binary.BigEndian.Uint16(msg[4:6]))
	if qdCount < 1 {
		return 0
	}
	offset := 12
	for i := 0; i < qdCount; i++ {
		for {
			if offset >= len(msg) {
				return 0
			}
			n := int(msg[offset])
			offset++
			if n == 0 {
				break
			}
			if n&0xc0 != 0 {
				// Compression is legal in responses, but not in normal client
				// question names. Reject it here to avoid pointer loops.
				return 0
			}
			if n > 63 || offset+n > len(msg) {
				return 0
			}
			offset += n
		}
		if offset+4 > len(msg) {
			return 0
		}
		offset += 4
	}
	return offset
}

func servfail(query []byte) []byte {
	end := questionEnd(query)
	if end == 0 {
		end = 12
	}
	resp := make([]byte, end)
	copy(resp, query[:end])
	resp[2] |= 0x80
	resp[3] = (resp[3] & 0xf0) | 0x02
	clear(resp[6:12])
	return resp
}

type dohUpstream struct {
	name   string
	url    string
	client *http.Client
}

func newDoHUpstream(cfg config.DNSUpstreamConfig, planner listener.RoutePlanner) (*dohUpstream, error) {
	parsed, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, err
	}
	target := parsed.Host
	if parsed.Port() == "" {
		target = net.JoinHostPort(parsed.Hostname(), "443")
	}
	ep, err := newRoutedEndpoint(planner, target, cfg.BootstrapIPs)
	if err != nil {
		return nil, err
	}
	if err := ep.validateNoLocalResolve("tcp"); err != nil {
		return nil, err
	}
	tlsCfg := newTLSConfig(tls.VersionTLS12, serverNameFor(parsed.Hostname(), cfg.ServerName))
	transport := &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   tlsCfg,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" {
				return nil, fmt.Errorf("unsupported DoH network %q", network)
			}
			return ep.dialTCP(ctx, address)
		},
	}
	return &dohUpstream{
		name: upstreamName(cfg.Name, "doh:"+parsed.Hostname()),
		url:  cfg.URL,
		client: &http.Client{
			Transport: transport,
		},
	}, nil
}

func (u *dohUpstream) Name() string { return u.name }

func (u *dohUpstream) Exchange(ctx context.Context, query []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.url, bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	req.ContentLength = int64(len(query))

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDNSMessage+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxDNSMessage {
		return nil, errors.New("DoH response too large")
	}
	return body, nil
}

func (u *dohUpstream) Close() error {
	if u.client != nil {
		if tr, ok := u.client.Transport.(*http.Transport); ok {
			tr.CloseIdleConnections()
		}
	}
	return nil
}

type dotUpstream struct {
	name      string
	target    string
	endpoint  routedEndpoint
	tlsConfig *tls.Config
}

func newDoTUpstream(cfg config.DNSUpstreamConfig, planner listener.RoutePlanner) (*dotUpstream, error) {
	ep, err := newRoutedEndpoint(planner, cfg.Address, cfg.BootstrapIPs)
	if err != nil {
		return nil, err
	}
	if err := ep.validateNoLocalResolve("tcp"); err != nil {
		return nil, err
	}
	return &dotUpstream{
		name:      upstreamName(cfg.Name, "dot:"+ep.host),
		target:    cfg.Address,
		endpoint:  ep,
		tlsConfig: newTLSConfig(tls.VersionTLS12, serverNameFor(ep.host, cfg.ServerName)),
	}, nil
}

func (u *dotUpstream) Name() string { return u.name }

func (u *dotUpstream) Exchange(ctx context.Context, query []byte) ([]byte, error) {
	raw, err := u.endpoint.dialTCP(ctx, u.target)
	if err != nil {
		return nil, err
	}
	defer raw.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(deadline)
	}
	conn := tls.Client(raw, u.tlsConfig.Clone())
	if err := conn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := writeDNSFrame(conn, query); err != nil {
		return nil, err
	}
	return readDNSFrame(conn)
}

func (u *dotUpstream) Close() error { return nil }

type doqUpstream struct {
	name      string
	target    string
	endpoint  routedEndpoint
	tlsConfig *tls.Config
	quicConf  *quic.Config

	mu        sync.Mutex
	packet    net.PacketConn
	transport *quic.Transport
	conn      *quic.Conn
}

func newDoQUpstream(cfg config.DNSUpstreamConfig, planner listener.RoutePlanner) (*doqUpstream, error) {
	ep, err := newRoutedEndpoint(planner, cfg.Address, cfg.BootstrapIPs)
	if err != nil {
		return nil, err
	}
	if err := ep.validateNoLocalResolve("udp"); err != nil {
		return nil, err
	}
	return &doqUpstream{
		name:      upstreamName(cfg.Name, "doq:"+ep.host),
		target:    cfg.Address,
		endpoint:  ep,
		tlsConfig: newTLSConfig(tls.VersionTLS13, serverNameFor(ep.host, cfg.ServerName), "doq"),
		quicConf: &quic.Config{
			HandshakeIdleTimeout: 5 * time.Second,
			MaxIdleTimeout:       30 * time.Second,
			KeepAlivePeriod:      15 * time.Second,
		},
	}, nil
}

func (u *doqUpstream) Name() string { return u.name }

func (u *doqUpstream) Exchange(ctx context.Context, query []byte) ([]byte, error) {
	id := dnsID(query)
	wire := append([]byte(nil), query...)
	setDNSID(wire, [2]byte{})

	resp, err := u.exchangeOnce(ctx, wire)
	if err != nil {
		u.reset()
		resp, err = u.exchangeOnce(ctx, wire)
		if err != nil {
			return nil, err
		}
	}
	setDNSID(resp, id)
	return resp, nil
}

func (u *doqUpstream) exchangeOnce(ctx context.Context, query []byte) ([]byte, error) {
	conn, err := u.connection(ctx)
	if err != nil {
		return nil, err
	}
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	if err := writeDNSFrame(stream, query); err != nil {
		stream.CancelWrite(1)
		return nil, err
	}
	if err := stream.Close(); err != nil {
		return nil, err
	}
	resp, err := readDNSFrame(stream)
	if err != nil {
		stream.CancelRead(1)
		return nil, err
	}
	return resp, nil
}

func (u *doqUpstream) connection(ctx context.Context) (*quic.Conn, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.conn != nil && u.conn.Context().Err() == nil {
		return u.conn, nil
	}
	u.closeLocked()
	pc, addr, err := u.endpoint.dialPacket(ctx)
	if err != nil {
		return nil, err
	}
	tr := &quic.Transport{Conn: pc}
	conn, err := tr.Dial(ctx, addr, u.tlsConfig.Clone(), u.quicConf)
	if err != nil {
		_ = tr.Close()
		_ = pc.Close()
		return nil, err
	}
	u.packet = pc
	u.transport = tr
	u.conn = conn
	return conn, nil
}

func (u *doqUpstream) reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.closeLocked()
}

func (u *doqUpstream) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.closeLocked()
}

func (u *doqUpstream) closeLocked() error {
	var errs []error
	if u.conn != nil {
		if err := u.conn.CloseWithError(0, "closed"); err != nil {
			errs = append(errs, err)
		}
		u.conn = nil
	}
	if u.transport != nil {
		if err := u.transport.Close(); err != nil {
			errs = append(errs, err)
		}
		u.transport = nil
	}
	if u.packet != nil {
		if err := u.packet.Close(); err != nil {
			errs = append(errs, err)
		}
		u.packet = nil
	}
	return errors.Join(errs...)
}

func writeDNSFrame(w io.Writer, msg []byte) error {
	if len(msg) > maxDNSMessage {
		return errors.New("DNS message too large")
	}
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(msg)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(msg)
	return err
}

func readDNSFrame(r io.Reader) ([]byte, error) {
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(lenBuf[:]))
	if n == 0 {
		return nil, errors.New("empty DNS response")
	}
	resp := make([]byte, n)
	if _, err := io.ReadFull(r, resp); err != nil {
		return nil, err
	}
	return resp, nil
}
