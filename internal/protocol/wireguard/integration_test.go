package wireguard

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/JohnThre/clambhook/internal/protocol"
)

// TestRoundTripTCPAndUDP wires two userspace WireGuard instances together
// over loopback UDP and verifies the dialer can actually carry traffic.
//
// Why bother: parseConfig + buildUAPIConfig unit tests catch syntax bugs,
// but the most likely class of regression is *integration* — a wrong
// IpcSet line, a wrong nonce convention, a wrong netstack address — that
// only fires when packets actually flow. This test does end-to-end TCP
// and UDP echoes through a real WireGuard handshake, ~1s on loopback.
//
// Two peers, both on userspace netstacks:
//
//	server: interior 10.0.0.1, listens on 127.0.0.1:<wgPort> for WG
//	client: interior 10.0.0.2, dials 127.0.0.1:<wgPort> as peer
//
// Server runs a TCP echo at 10.0.0.1:9000 and a UDP echo at 10.0.0.1:9001,
// both inside the server's netstack. Client uses the *real* dialer being
// tested.
func TestRoundTripTCPAndUDP(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	srvPrivB64, srvPubB64 := mustKeypair(t)
	cliPrivB64, cliPubB64 := mustKeypair(t)

	wgPort := mustFreeUDPPort(t)
	wgEndpoint := fmt.Sprintf("127.0.0.1:%d", wgPort)

	// Server side: bring up a WG netstack listening on the loopback UDP
	// port and start TCP/UDP echoes inside the netstack.
	srvDev, srvNet := mustBringUpServer(t, srvPrivB64, cliPubB64, wgPort)
	defer srvDev.Close()

	tcpReady := make(chan struct{})
	udpReady := make(chan struct{})
	go runTCPEcho(t, srvNet, "10.0.0.1:9000", tcpReady)
	go runUDPEcho(t, srvNet, "10.0.0.1:9001", udpReady)
	<-tcpReady
	<-udpReady

	// Client side: build a real dialer from a TOML-shaped config, exercise
	// Dial (TCP) and DialPacket (UDP) through it.
	d := mustBuildDialer(t, cliPrivB64, srvPubB64, wgEndpoint)
	defer func() {
		if d.inst != nil {
			_ = d.inst.Close()
		}
	}()

	t.Run("tcp", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, err := d.Dial(ctx, "tcp", "10.0.0.1:9000")
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

		want := []byte("hello wireguard tcp")
		if _, err := conn.Write(want); err != nil {
			t.Fatalf("Write: %v", err)
		}
		got := make([]byte, len(want))
		if _, err := io.ReadFull(conn, got); err != nil {
			t.Fatalf("Read: %v", err)
		}
		if string(got) != string(want) {
			t.Errorf("echo mismatch: got %q want %q", got, want)
		}
	})

	t.Run("udp", func(t *testing.T) {
		pc, err := d.DialPacket(context.Background(), "")
		if err != nil {
			t.Fatalf("DialPacket: %v", err)
		}
		defer pc.Close()
		_ = pc.SetDeadline(time.Now().Add(3 * time.Second))

		target, _ := net.ResolveUDPAddr("udp", "10.0.0.1:9001")
		want := []byte("hello wireguard udp")
		if _, err := pc.WriteTo(want, target); err != nil {
			t.Fatalf("WriteTo: %v", err)
		}
		buf := make([]byte, 1024)
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			t.Fatalf("ReadFrom: %v", err)
		}
		if string(buf[:n]) != string(want) {
			t.Errorf("echo mismatch: got %q want %q", buf[:n], want)
		}
		if from.String() != target.String() {
			t.Errorf("from = %s, want %s", from, target)
		}
	})
}

// TestDialThroughErrors verifies that the chained-as-final-hop path
// surfaces a clear error rather than failing silently or panicking.
func TestDialThroughErrors(t *testing.T) {
	d := &dialer{cfg: config{}}
	if _, err := d.DialThrough(context.Background(), nil, "1.2.3.4:80"); err == nil {
		t.Error("DialThrough: expected error, got nil")
	}
	if _, err := d.DialPacketThrough(context.Background(), nil, ""); err == nil {
		t.Error("DialPacketThrough: expected error, got nil")
	}
}

// mustKeypair generates a fresh Curve25519 keypair, returning each as
// base64 (the form parseConfig accepts at the TOML boundary).
func mustKeypair(t *testing.T) (privB64, pubB64 string) {
	t.Helper()
	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		t.Fatal(err)
	}
	// Standard Curve25519 clamping (RFC 7748 §5).
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(pub)
}

// mustFreeUDPPort grabs a free UDP port by binding+closing — the kernel
// won't reuse it instantly, so it's safe to hand to wireguard-go's bind.
func mustFreeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	c.Close()
	return port
}

// mustBringUpServer constructs a server-side WG instance bound to a
// specific UDP port (so the client knows where to dial). Uses the same
// netstack package as production code.
func mustBringUpServer(t *testing.T, srvPrivB64, cliPubB64 string, wgPort int) (*device.Device, *netstack.Net) {
	t.Helper()

	addrs := []netip.Addr{netip.MustParseAddr("10.0.0.1")}
	tunDev, tnet, err := netstack.CreateNetTUN(addrs, nil, device.DefaultMTU)
	if err != nil {
		t.Fatal(err)
	}

	// LogLevelSilent to keep test output clean; flip to LogLevelVerbose
	// when debugging handshake issues.
	logger := device.NewLogger(device.LogLevelSilent, "(server) ")
	dev := device.NewDevice(tunDev, conn.NewDefaultBind(), logger)

	srvPrivHex, _ := keyToHex(srvPrivB64)
	cliPubHex, _ := keyToHex(cliPubB64)

	uapi := fmt.Sprintf(
		"private_key=%s\nlisten_port=%d\npublic_key=%s\nallowed_ip=10.0.0.2/32\n",
		srvPrivHex, wgPort, cliPubHex,
	)
	if err := dev.IpcSet(uapi); err != nil {
		t.Fatalf("server IpcSet: %v", err)
	}
	if err := dev.Up(); err != nil {
		t.Fatalf("server Up: %v", err)
	}
	return dev, tnet
}

// mustBuildDialer constructs the production dialer from a config that
// points at the loopback WG server. parseConfig is invoked end-to-end so
// any binding or validation drift between the TOML schema and the dialer
// shows up here.
func mustBuildDialer(t *testing.T, cliPrivB64, srvPubB64, endpoint string) *dialer {
	t.Helper()
	s := protocol.Server{
		Name:    "wg-client",
		Address: endpoint,
		Settings: map[string]any{
			"private_key": cliPrivB64,
			"addresses":   []any{"10.0.0.2/32"},
			"peers": []map[string]any{
				{
					"public_key":  srvPubB64,
					"endpoint":    endpoint,
					"allowed_ips": []any{"10.0.0.0/24"},
				},
			},
		},
	}
	cfg, err := parseConfig(s)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	return &dialer{server: s, cfg: cfg}
}

// runTCPEcho stands up an in-netstack TCP listener and echoes one
// connection's bytes back. Closes after one client.
func runTCPEcho(t *testing.T, tnet *netstack.Net, addr string, ready chan<- struct{}) {
	t.Helper()
	host, portStr, _ := net.SplitHostPort(addr)
	port := mustParsePort(t, portStr)
	ip := netip.MustParseAddr(host)

	ln, err := tnet.ListenTCPAddrPort(netip.AddrPortFrom(ip, uint16(port)))
	if err != nil {
		t.Errorf("server tcp listen: %v", err)
		close(ready)
		return
	}
	close(ready)

	c, err := ln.Accept()
	if err != nil {
		return
	}
	defer c.Close()
	defer ln.Close()

	buf := make([]byte, 1024)
	n, err := c.Read(buf)
	if err != nil && err != io.EOF {
		return
	}
	c.Write(buf[:n])
}

// runUDPEcho echoes back one datagram from a netstack UDP socket.
func runUDPEcho(t *testing.T, tnet *netstack.Net, addr string, ready chan<- struct{}) {
	t.Helper()
	host, portStr, _ := net.SplitHostPort(addr)
	port := mustParsePort(t, portStr)
	ip := netip.MustParseAddr(host)

	pc, err := tnet.ListenUDPAddrPort(netip.AddrPortFrom(ip, uint16(port)))
	if err != nil {
		t.Errorf("server udp listen: %v", err)
		close(ready)
		return
	}
	close(ready)
	defer pc.Close()

	buf := make([]byte, 1024)
	n, src, err := pc.ReadFrom(buf)
	if err != nil {
		return
	}
	pc.WriteTo(buf[:n], src)
}

func mustParsePort(t *testing.T, s string) int {
	t.Helper()
	var p int
	if _, err := fmt.Sscanf(s, "%d", &p); err != nil {
		t.Fatal(err)
	}
	return p
}
