package vless

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/clambhook/clambhook/internal/protocol"
	"github.com/clambhook/clambhook/internal/protocol/v2ray"
	"github.com/google/uuid"
)

const testUUID = "b831381d-6324-4d53-ad4f-8cda48b30811"

func TestDialerRegistered(t *testing.T) {
	d, err := protocol.NewDialer(protocol.Server{
		Name:     "test",
		Address:  "example.com:443",
		Protocol: "vless",
		Settings: map[string]any{"uuid": testUUID},
	})
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	if d.Protocol() != "vless" {
		t.Errorf("Protocol() = %q, want vless", d.Protocol())
	}
	if _, ok := d.(protocol.PacketDialer); !ok {
		t.Error("dialer does not implement PacketDialer")
	}
}

// TestTCPRoundTripThroughTLS runs a complete VLESS TCP handshake against an
// in-process TLS server. The server reads the VLESS request header, echoes
// the response header, then echoes any payload. Exercises the full path:
// TLS wrap → request encode → response parse → bidirectional data.
func TestTCPRoundTripThroughTLS(t *testing.T) {
	cert, pool := newTestCert(t)
	ln := tlsListener(t, cert)
	defer ln.Close()

	var serverErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		serverErr = runFakeVLESSServer(ln, cmdTCP)
	}()

	id := uuid.MustParse(testUUID)
	d := &dialer{
		server: protocol.Server{Address: ln.Addr().String()},
		cfg: config{
			uuid:       id,
			flow:       "none",
			sni:        "example.com",
			alpn:       []string{"h2"},
			skipVerify: false,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Override the cert pool for the test handshake (skip_cert_verify is for
	// real deployments; here we use a real verified handshake against a
	// self-signed cert we trust explicitly).
	tlsConn := tls.Client(raw, &tls.Config{
		ServerName: "example.com",
		NextProtos: []string{"h2"},
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		t.Fatalf("tls handshake: %v", err)
	}

	header, err := encodeRequest(d.cfg.uuid, cmdTCP, "example.org:80")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := tlsConn.Write(header); err != nil {
		t.Fatalf("write header: %v", err)
	}

	c := &conn{Conn: tlsConn}
	payload := []byte("hello vless")
	if _, err := c.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(buf[:n], payload) {
		t.Errorf("echo mismatch: got %q, want %q", buf[:n], payload)
	}
	c.Close()

	wg.Wait()
	if serverErr != nil {
		t.Errorf("server: %v", serverErr)
	}
}

// TestUDPRoundTripThroughTLS exercises the UDP framing path: length-prefixed
// datagrams over a VLESS session opened with cmd=UDP.
func TestUDPRoundTripThroughTLS(t *testing.T) {
	cert, pool := newTestCert(t)
	ln := tlsListener(t, cert)
	defer ln.Close()

	var serverErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		serverErr = runFakeVLESSServer(ln, cmdUDP)
	}()

	id := uuid.MustParse(testUUID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	tlsConn := tls.Client(raw, &tls.Config{
		ServerName: "example.com",
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		t.Fatalf("tls handshake: %v", err)
	}
	header, err := encodeRequest(id, cmdUDP, "1.1.1.1:53")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := tlsConn.Write(header); err != nil {
		t.Fatalf("write header: %v", err)
	}

	pc := &packetConn{stream: tlsConn, target: "1.1.1.1:53"}
	payload := []byte{0xde, 0xad, 0xbe, 0xef}
	if _, err := pc.WriteTo(payload, &net.UDPAddr{IP: net.ParseIP("1.1.1.1"), Port: 53}); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	buf := make([]byte, 64)
	n, addr, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if !bytes.Equal(buf[:n], payload) {
		t.Errorf("udp echo mismatch: got %x, want %x", buf[:n], payload)
	}
	if addr.String() != "1.1.1.1:53" {
		t.Errorf("addr = %q, want 1.1.1.1:53", addr.String())
	}
	pc.Close()

	wg.Wait()
	if serverErr != nil {
		t.Errorf("server: %v", serverErr)
	}
}

// runFakeVLESSServer accepts one TLS connection, parses the VLESS request
// header, writes a response header (version 0x00 + no addons), then behaves
// as an echo server — for TCP it echoes raw stream bytes, for UDP it echoes
// length-prefixed datagrams.
func runFakeVLESSServer(ln net.Listener, wantCmd byte) error {
	c, err := ln.Accept()
	if err != nil {
		return err
	}
	defer c.Close()

	// Read + validate request header.
	head := make([]byte, 1+16+1+1+2+1) // ver+uuid+addonlen+cmd+port+atyp
	if _, err := io.ReadFull(c, head); err != nil {
		return err
	}
	if head[0] != version {
		return fmt.Errorf("bad version %#x", head[0])
	}
	if head[17] != 0x00 {
		return fmt.Errorf("addon_len = %d, want 0", head[17])
	}
	if head[18] != wantCmd {
		return fmt.Errorf("cmd = %#x, want %#x", head[18], wantCmd)
	}
	// Skip address bytes.
	switch head[21] {
	case v2ray.ATYPIPv4:
		if _, err := io.ReadFull(c, make([]byte, 4)); err != nil {
			return err
		}
	case v2ray.ATYPIPv6:
		if _, err := io.ReadFull(c, make([]byte, 16)); err != nil {
			return err
		}
	case v2ray.ATYPDomain:
		var lb [1]byte
		if _, err := io.ReadFull(c, lb[:]); err != nil {
			return err
		}
		if _, err := io.ReadFull(c, make([]byte, int(lb[0]))); err != nil {
			return err
		}
	default:
		return fmt.Errorf("bad atyp %#x", head[21])
	}

	// Respond: ver=0x00, addon_len=0x00.
	if _, err := c.Write([]byte{0x00, 0x00}); err != nil {
		return err
	}

	// Echo.
	if wantCmd == cmdTCP {
		// Simple stream echo until client closes.
		_, _ = io.Copy(c, c)
		return nil
	}

	// UDP echo: read [len][payload], write [len][payload].
	var lb [2]byte
	if _, err := io.ReadFull(c, lb[:]); err != nil {
		return err
	}
	n := int(binary.BigEndian.Uint16(lb[:]))
	buf := make([]byte, n)
	if _, err := io.ReadFull(c, buf); err != nil {
		return err
	}
	frame := make([]byte, 0, 2+n)
	frame = binary.BigEndian.AppendUint16(frame, uint16(n))
	frame = append(frame, buf...)
	if _, err := c.Write(frame); err != nil {
		return err
	}
	return nil
}

// tlsListener returns a TLS listener bound to 127.0.0.1:0.
func tlsListener(t *testing.T, cert tls.Certificate) net.Listener {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

// newTestCert generates a self-signed ECDSA cert for "example.com", returning
// the tls.Certificate for the server and an x509 pool the client can trust.
func newTestCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"example.com"},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("append cert to pool failed")
	}
	return cert, pool
}

