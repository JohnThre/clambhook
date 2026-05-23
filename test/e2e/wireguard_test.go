//go:build e2e

package e2e

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"testing"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	_ "github.com/JohnThre/clambhook/internal/protocol/wireguard"
)

func TestWireGuardCompatibility(t *testing.T) {
	requireE2E(t)

	srvPrivB64, srvPubB64 := wgKeypair(t)
	cliPrivB64, cliPubB64 := wgKeypair(t)
	wgPort := mustFreePort(t)
	endpoint := net.JoinHostPort("127.0.0.1", strconv.Itoa(wgPort))

	srvDev, srvNet := startWireGuardServer(t, srvPrivB64, cliPubB64, wgPort)
	t.Cleanup(srvDev.Close)

	tcpReady := make(chan struct{})
	udpReady := make(chan struct{})
	go runWGTCPEcho(t, srvNet, "10.82.0.1:9000", tcpReady)
	go runWGUDPEcho(t, srvNet, "10.82.0.1:9001", udpReady)
	<-tcpReady
	<-udpReady

	wgNode := node("wg", endpoint, "wireguard", map[string]any{
		"private_key": cliPrivB64,
		"addresses":   []any{"10.82.0.2/32"},
		"log_level":   "silent",
		"peers": []map[string]any{{
			"public_key":  srvPubB64,
			"endpoint":    endpoint,
			"allowed_ips": []any{"10.82.0.0/24"},
		}},
	})
	ch := singleNodeChain("wireguard-e2e", wgNode)
	defer ch.Close()

	t.Run("tcp", func(t *testing.T) {
		assertTCPRoundTrip(t, ch, "10.82.0.1:9000")
	})
	t.Run("udp", func(t *testing.T) {
		assertUDPRoundTrip(t, ch, "", "10.82.0.1:9001")
	})
}

func wgKeypair(t *testing.T) (privB64, pubB64 string) {
	t.Helper()
	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		t.Fatal(err)
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(pub)
}

func wgKeyToHex(t *testing.T, keyB64 string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 32 {
		t.Fatalf("wireguard key length = %d, want 32", len(b))
	}
	return hex.EncodeToString(b)
}

func startWireGuardServer(t *testing.T, srvPrivB64, cliPubB64 string, wgPort int) (*device.Device, *netstack.Net) {
	t.Helper()
	tunDev, tnet, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.82.0.1")}, nil, device.DefaultMTU)
	if err != nil {
		t.Fatal(err)
	}
	dev := device.NewDevice(tunDev, conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, "(e2e-wg-server) "))
	uapi := fmt.Sprintf(
		"private_key=%s\nlisten_port=%d\npublic_key=%s\nallowed_ip=10.82.0.2/32\n",
		wgKeyToHex(t, srvPrivB64), wgPort, wgKeyToHex(t, cliPubB64),
	)
	if err := dev.IpcSet(uapi); err != nil {
		t.Fatal(err)
	}
	if err := dev.Up(); err != nil {
		t.Fatal(err)
	}
	return dev, tnet
}

func runWGTCPEcho(t *testing.T, tnet *netstack.Net, addr string, ready chan<- struct{}) {
	t.Helper()
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	ln, err := tnet.ListenTCPAddrPort(netip.AddrPortFrom(netip.MustParseAddr(host), uint16(port)))
	if err != nil {
		t.Errorf("wg tcp listen: %v", err)
		close(ready)
		return
	}
	close(ready)
	defer ln.Close()
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
}

func runWGUDPEcho(t *testing.T, tnet *netstack.Net, addr string, ready chan<- struct{}) {
	t.Helper()
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	udp, err := tnet.ListenUDPAddrPort(netip.AddrPortFrom(netip.MustParseAddr(host), uint16(port)))
	if err != nil {
		t.Errorf("wg udp listen: %v", err)
		close(ready)
		return
	}
	close(ready)
	buf := make([]byte, 2048)
	for {
		n, from, err := udp.ReadFrom(buf)
		if err != nil {
			return
		}
		_, _ = udp.WriteTo(buf[:n], from)
	}
}
