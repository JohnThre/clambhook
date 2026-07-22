//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/protocol"
)

const e2eTimeout = 20 * time.Second

func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("CLAMBHOOK_E2E") != "1" {
		t.Skip("set CLAMBHOOK_E2E=1 to run real-server e2e tests")
	}
}

func e2eRequired() bool {
	return os.Getenv("CLAMBHOOK_E2E_REQUIRE") == "1"
}

func skipOrFatal(t *testing.T, format string, args ...any) {
	t.Helper()
	msg := fmt.Sprintf(format, args...)
	if e2eRequired() {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

func mustFreeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func mustFreePort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 20; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
		_ = ln.Close()
		if err == nil {
			_ = udp.Close()
			return port
		}
	}
	t.Fatal("could not allocate a free TCP+UDP port")
	return 0
}

func waitTCP(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return nil
		}
		last = err
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("wait tcp %s: %w", addr, last)
}

type tcpEcho struct {
	addr string
	ln   net.Listener
	done chan struct{}
}

func startTCPEcho(t *testing.T) *tcpEcho {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &tcpEcho{addr: ln.Addr().String(), ln: ln, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-s.done
	})
	return s
}

type udpEcho struct {
	addr string
	conn *net.UDPConn
	done chan struct{}
}

func startUDPEcho(t *testing.T) *udpEcho {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	s := &udpEcho{addr: conn.LocalAddr().String(), conn: conn, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		buf := make([]byte, 65535)
		for {
			n, src, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP(buf[:n], src)
		}
	}()
	t.Cleanup(func() {
		_ = conn.Close()
		<-s.done
	})
	return s
}

func assertTCPRoundTrip(t *testing.T, ch *chain.Chain, target string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), e2eTimeout)
	defer cancel()
	conn, err := ch.Dial(ctx, "tcp", target)
	if err != nil {
		t.Fatalf("chain Dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	payload := []byte("clambhook-e2e-tcp-" + t.Name())
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("tcp write: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("tcp read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("tcp echo = %q, want %q", got, payload)
	}
}

func assertUDPRoundTrip(t *testing.T, ch *chain.Chain, sessionTarget, datagramTarget string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), e2eTimeout)
	defer cancel()
	pc, err := ch.DialPacket(ctx, sessionTarget)
	if err != nil {
		t.Fatalf("chain DialPacket: %v", err)
	}
	defer pc.Close()
	_ = pc.SetDeadline(time.Now().Add(5 * time.Second))
	target, err := net.ResolveUDPAddr("udp", datagramTarget)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("clambhook-e2e-udp-" + t.Name())
	if _, err := pc.WriteTo(payload, target); err != nil {
		t.Fatalf("udp write: %v", err)
	}
	buf := make([]byte, 2048)
	n, from, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("udp read: %v", err)
	}
	if !bytes.Equal(buf[:n], payload) {
		t.Fatalf("udp echo = %q, want %q", buf[:n], payload)
	}
	if from == nil || from.String() == "" {
		t.Fatalf("udp source is empty: %v", from)
	}
}

func node(name, address, proto string, settings map[string]any) protocol.Server {
	return protocol.Server{Name: name, Address: address, Protocol: proto, Settings: settings}
}

func singleNodeChain(name string, srv protocol.Server) *chain.Chain {
	return &chain.Chain{Name: name, Nodes: []protocol.Server{srv}}
}

type managedCmd struct {
	cancel context.CancelFunc
	done   chan error
	output *bytes.Buffer
}

func startCommand(t *testing.T, name string, args ...string) *managedCmd {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, name, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start %s: %v", name, err)
	}
	m := &managedCmd{cancel: cancel, done: make(chan error, 1), output: &output}
	go func() { m.done <- cmd.Wait() }()
	t.Cleanup(func() {
		m.cancel()
		select {
		case <-m.done:
		case <-time.After(5 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-m.done
		}
		if t.Failed() {
			t.Logf("%s output:\n%s", name, m.output.String())
		}
	})
	return m
}

func writeSelfSignedCert(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath = filepath.Join(dir, "server-cert.pem")
	keyPath = filepath.Join(dir, "server-key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func writeOpenVPNPKI(t *testing.T, dir string) (caPEM, serverCertPEM, serverKeyPEM, clientCertPEM, clientKeyPEM string) {
	t.Helper()

	notBefore := time.Now().Add(-time.Hour)
	notAfter := time.Now().Add(24 * time.Hour)

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "clambhook-test-openvpn-ca"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1").To4()},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTpl, caTpl, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	serverKeyDER, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}

	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientTpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "clambhook-test-openvpn-client"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTpl, caTpl, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	clientKeyDER, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}

	write := func(name string, blockType string, der []byte) string {
		path := filepath.Join(dir, name)
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
		if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		return string(pemBytes)
	}

	caPEM = write("ca.pem", "CERTIFICATE", caDER)
	serverCertPEM = write("server-cert.pem", "CERTIFICATE", serverDER)
	serverKeyPEM = write("server-key.pem", "PRIVATE KEY", serverKeyDER)
	clientCertPEM = write("client-cert.pem", "CERTIFICATE", clientDER)
	clientKeyPEM = write("client-key.pem", "PRIVATE KEY", clientKeyDER)
	return
}

func socks5TCPRoundTrip(t *testing.T, proxy, target string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxy, 5*time.Second)
	if err != nil {
		t.Fatalf("dial socks5: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	socks5Handshake(t, conn)
	socks5Connect(t, conn, target)
	payload := []byte("clambhook-e2e-socks-tcp")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("socks tcp write: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("socks tcp read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("socks tcp echo = %q, want %q", got, payload)
	}
}

func socks5UDPRoundTrip(t *testing.T, proxy, target string) {
	t.Helper()
	control, err := net.DialTimeout("tcp", proxy, 5*time.Second)
	if err != nil {
		t.Fatalf("dial socks5: %v", err)
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(5 * time.Second))
	socks5Handshake(t, control)

	if _, err := control.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatalf("write UDP ASSOCIATE: %v", err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(control, reply); err != nil {
		t.Fatalf("read UDP ASSOCIATE reply: %v", err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("UDP ASSOCIATE reply = %#x, want success", reply[1])
	}
	relay := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(binary.BigEndian.Uint16(reply[8:10]))}
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	payload := []byte("clambhook-e2e-socks-udp")
	frame := encodeSocks5UDP(t, target, payload)
	if _, err := client.WriteToUDP(frame, relay); err != nil {
		t.Fatalf("write socks udp: %v", err)
	}
	buf := make([]byte, 2048)
	n, _, err := client.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read socks udp: %v", err)
	}
	got := decodeSocks5UDP(t, buf[:n])
	if !bytes.Equal(got, payload) {
		t.Fatalf("socks udp echo = %q, want %q", got, payload)
	}
}

func socks5UDPUnsupported(t *testing.T, proxy string) {
	t.Helper()
	control, err := net.DialTimeout("tcp", proxy, 5*time.Second)
	if err != nil {
		t.Fatalf("dial socks5: %v", err)
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(5 * time.Second))
	socks5Handshake(t, control)
	if _, err := control.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatalf("write UDP ASSOCIATE: %v", err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(control, reply); err != nil {
		t.Fatalf("read UDP ASSOCIATE reply: %v", err)
	}
	if reply[1] != 0x07 {
		t.Fatalf("UDP ASSOCIATE reply = %#x, want command-not-supported", reply[1])
	}
}

func socks5Handshake(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write socks greeting: %v", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read socks greeting: %v", err)
	}
	if !bytes.Equal(reply, []byte{0x05, 0x00}) {
		t.Fatalf("socks greeting reply = %x", reply)
	}
}

func socks5Connect(t *testing.T, conn net.Conn, target string) {
	t.Helper()
	req := encodeSocks5Addr(t, target, []byte{0x05, 0x01, 0x00})
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write socks connect: %v", err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read socks connect reply: %v", err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("socks connect reply = %#x", reply[1])
	}
}

func encodeSocks5UDP(t *testing.T, target string, payload []byte) []byte {
	t.Helper()
	return encodeSocks5Addr(t, target, []byte{0, 0, 0}, payload)
}

func encodeSocks5Addr(t *testing.T, target string, prefix []byte, payload ...[]byte) []byte {
	t.Helper()
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		t.Fatalf("bad target port %q", portStr)
	}
	out := append([]byte{}, prefix...)
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			out = append(out, 0x01)
			out = append(out, v4...)
		} else {
			out = append(out, 0x04)
			out = append(out, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			t.Fatalf("bad target host %q", host)
		}
		out = append(out, 0x03, byte(len(host)))
		out = append(out, host...)
	}
	out = binary.BigEndian.AppendUint16(out, uint16(port))
	for _, p := range payload {
		out = append(out, p...)
	}
	return out
}

func decodeSocks5UDP(t *testing.T, frame []byte) []byte {
	t.Helper()
	if len(frame) < 4 || frame[2] != 0 {
		t.Fatalf("bad socks udp frame: %x", frame)
	}
	idx := 4
	switch frame[3] {
	case 0x01:
		idx += 4
	case 0x04:
		idx += 16
	case 0x03:
		if idx >= len(frame) {
			t.Fatalf("short domain frame: %x", frame)
		}
		idx += 1 + int(frame[idx])
	default:
		t.Fatalf("bad socks udp atyp %#x", frame[3])
	}
	idx += 2
	if idx > len(frame) {
		t.Fatalf("short socks udp frame: %x", frame)
	}
	return frame[idx:]
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func dockerUsable(ctx context.Context) bool {
	if !commandExists("docker") {
		return false
	}
	cmd := exec.CommandContext(ctx, "docker", "version")
	return cmd.Run() == nil
}

func ignoreClosed(err error) bool {
	return err == nil || errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection")
}
