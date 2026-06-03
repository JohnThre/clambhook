package dnsproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/listener"
	"github.com/quic-go/quic-go"
)

func TestDoHExchange(t *testing.T) {
	withInsecureTLS(t)
	query := testDNSQuery(0x1234)
	response := testDNSResponse(query)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/dns-message" {
			t.Errorf("content-type = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if !bytes.Equal(body, query) {
			t.Errorf("query body = %x, want %x", body, query)
		}
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(response)
	}))
	defer server.Close()

	proxy, err := New(config.DNSConfig{
		Enabled: true,
		Timeout: config.Duration(2 * time.Second),
		Upstreams: []config.DNSUpstreamConfig{{
			Protocol: "doh",
			URL:      server.URL,
		}},
	}, directPlanner{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer proxy.Close()

	got, err := proxy.Exchange(context.Background(), query)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if !bytes.Equal(got, response) {
		t.Fatalf("response = %x, want %x", got, response)
	}
}

func TestDoTExchange(t *testing.T) {
	withInsecureTLS(t)
	query := testDNSQuery(0x2222)
	response := testDNSResponse(query)
	ln := startDoTTestServer(t, query, response)

	proxy, err := New(config.DNSConfig{
		Enabled: true,
		Timeout: config.Duration(2 * time.Second),
		Upstreams: []config.DNSUpstreamConfig{{
			Protocol: "dot",
			Address:  ln.Addr().String(),
		}},
	}, directPlanner{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer proxy.Close()

	got, err := proxy.Exchange(context.Background(), query)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if !bytes.Equal(got, response) {
		t.Fatalf("response = %x, want %x", got, response)
	}
}

func TestDoQExchangeRestoresClientID(t *testing.T) {
	withInsecureTLS(t)
	query := testDNSQuery(0xbeef)
	serverErr := startDoQTestServer(t)

	proxy, err := New(config.DNSConfig{
		Enabled: true,
		Timeout: config.Duration(2 * time.Second),
		Upstreams: []config.DNSUpstreamConfig{{
			Protocol: "doq",
			Address:  serverErr.addr,
		}},
	}, directPlanner{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer proxy.Close()

	got, err := proxy.Exchange(context.Background(), query)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if id := binary.BigEndian.Uint16(got[:2]); id != 0xbeef {
		t.Fatalf("response ID = %#x, want original client ID", id)
	}
	select {
	case err := <-serverErr.errs:
		if err != nil {
			t.Fatal(err)
		}
	default:
	}
}

func TestDirectHostnameRequiresBootstrapIPs(t *testing.T) {
	_, err := New(config.DNSConfig{
		Enabled: true,
		Upstreams: []config.DNSUpstreamConfig{{
			Protocol: "dot",
			Address:  "dns.example:853",
		}},
	}, directPlanner{})
	if err == nil || !strings.Contains(err.Error(), "needs bootstrap_ips") {
		t.Fatalf("New error = %v, want bootstrap guard", err)
	}
}

func TestExchangeReturnsServfailAfterUpstreamFailure(t *testing.T) {
	query := testDNSQuery(0x4444)
	proxy := &Proxy{
		timeout:   time.Second,
		upstreams: []upstream{failingUpstream{}},
	}

	resp, err := proxy.Exchange(context.Background(), query)
	if err == nil {
		t.Fatal("Exchange error = nil, want upstream error")
	}
	if len(resp) < 12 {
		t.Fatalf("response too short: %d", len(resp))
	}
	if id := binary.BigEndian.Uint16(resp[:2]); id != 0x4444 {
		t.Fatalf("response ID = %#x, want query ID", id)
	}
	if rcode := resp[3] & 0x0f; rcode != 2 {
		t.Fatalf("rcode = %d, want SERVFAIL", rcode)
	}
}

type directPlanner struct{}

func (directPlanner) DefaultChainName() string { return "direct" }

func (directPlanner) Plan(_ context.Context, network, target string) (listener.RoutePlan, error) {
	plan := listener.RoutePlan{
		Action:  listener.RouteActionDirect,
		Target:  target,
		Network: network,
	}
	switch network {
	case "tcp":
		plan.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}
	case "udp":
		plan.DialPacket = func(ctx context.Context, _ string) (net.PacketConn, error) {
			var lc net.ListenConfig
			return lc.ListenPacket(ctx, "udp", "127.0.0.1:0")
		}
	}
	return plan, nil
}

type failingUpstream struct{}

func (failingUpstream) Name() string { return "fail" }
func (failingUpstream) Exchange(context.Context, []byte) ([]byte, error) {
	return nil, errors.New("boom")
}
func (failingUpstream) Close() error { return nil }

func withInsecureTLS(t *testing.T) {
	t.Helper()
	orig := configureTLSForTest
	configureTLSForTest = func(cfg *tls.Config) {
		cfg.InsecureSkipVerify = true
	}
	t.Cleanup(func() { configureTLSForTest = orig })
}

func startDoTTestServer(t *testing.T, wantQuery, response []byte) net.Listener {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", testServerTLSConfig(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		got, err := readDNSFrame(conn)
		if err != nil {
			t.Errorf("DoT read frame: %v", err)
			return
		}
		if !bytes.Equal(got, wantQuery) {
			t.Errorf("DoT query = %x, want %x", got, wantQuery)
		}
		if err := writeDNSFrame(conn, response); err != nil {
			t.Errorf("DoT write frame: %v", err)
		}
	}()
	return ln
}

type doqTestServer struct {
	addr string
	errs chan error
}

func startDoQTestServer(t *testing.T) doqTestServer {
	t.Helper()
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	tr := &quic.Transport{Conn: udp}
	ln, err := tr.Listen(testServerTLSConfig(t, []string{"doq"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
		_ = tr.Close()
		_ = udp.Close()
	})
	out := doqTestServer{addr: udp.LocalAddr().String(), errs: make(chan error, 1)}
	go func() {
		conn, err := ln.Accept(context.Background())
		if err != nil {
			return
		}
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			out.errs <- err
			return
		}
		query, err := readDNSFrame(stream)
		if err != nil {
			out.errs <- err
			return
		}
		if id := binary.BigEndian.Uint16(query[:2]); id != 0 {
			out.errs <- errors.New("DoQ query ID was not zero")
			return
		}
		resp := testDNSResponse(query)
		if err := writeDNSFrame(stream, resp); err != nil {
			out.errs <- err
			return
		}
		_ = stream.Close()
		out.errs <- nil
	}()
	return out
}

func testServerTLSConfig(t *testing.T, nextProtos []string) *tls.Config {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: nextProtos}
}

func testDNSQuery(id uint16) []byte {
	q := []byte{
		byte(id >> 8), byte(id), 0x01, 0x00, 0x00, 0x01, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		7, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
		3, 'c', 'o', 'm',
		0,
		0x00, 0x01,
		0x00, 0x01,
	}
	return q
}

func testDNSResponse(query []byte) []byte {
	end := questionEnd(query)
	resp := make([]byte, end)
	copy(resp, query[:end])
	resp[2] = 0x81
	resp[3] = 0x80
	return resp
}
