package openvpn

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	appconfig "github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

const (
	openVPNLifecycleTargetIP      = "10.65.0.1"
	openVPNLifecycleTargetPort    = 9000
	openVPNLifecycleUDPTargetPort = 9001
)

func TestOpenVPNDialerCloseBeforeFirstDialRejectsFutureDials(t *testing.T) {
	oldFactory := newOpenVPNInstance
	factoryCalls := 0
	newOpenVPNInstance = func(context.Context, *config) (*instance, error) {
		factoryCalls++
		return nil, errors.New("unexpected factory call")
	}
	t.Cleanup(func() { newOpenVPNInstance = oldFactory })

	d := &dialer{cfg: &config{}}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	_, err := d.Dial(context.Background(), "tcp", openVPNLifecycleTarget())
	if err == nil || !strings.Contains(err.Error(), "dialer closed") {
		t.Fatalf("Dial after Close err = %v, want dialer closed", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls = %d, want 0", factoryCalls)
	}
}

func TestOpenVPNInstanceReusedAndClosedOnEngineReload(t *testing.T) {
	factory := installOpenVPNLifecycleFactory(t)

	oldAddr := freeOpenVPNLifecycleTCPAddr(t)
	newAddr := freeOpenVPNLifecycleTCPAddr(t)
	oldRemote := "127.0.0.1:1194"
	newRemote := "127.0.0.1:1195"
	e := engine.New(openVPNLifecycleConfig(t, "A", oldAddr, oldRemote), nil)
	t.Cleanup(func() { _ = e.Stop() })

	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	openVPNLifecycleSOCKSConnectEcho(t, oldAddr, []byte("first"))
	openVPNLifecycleSOCKSConnectEcho(t, oldAddr, []byte("second"))
	if got := factory.createCount(oldRemote); got != 1 {
		t.Fatalf("old instance creates = %d, want 1", got)
	}

	if err := e.Reload(openVPNLifecycleConfig(t, "B", newAddr, newRemote)); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := factory.closeCount(oldRemote); got != 1 {
		t.Fatalf("old instance closes = %d, want 1", got)
	}

	openVPNLifecycleSOCKSConnectEcho(t, newAddr, []byte("third"))
	if got := factory.createCount(newRemote); got != 1 {
		t.Fatalf("new instance creates = %d, want 1", got)
	}
	if err := e.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := factory.closeCount(newRemote); got != 1 {
		t.Fatalf("new instance closes = %d, want 1", got)
	}
}

func TestOpenVPNDialPacketUsesNetstack(t *testing.T) {
	installOpenVPNLifecycleFactory(t)
	d := &dialer{cfg: &config{remote: "127.0.0.1:1194", tunMTU: 1500}}
	defer d.Close()

	pc, err := d.DialPacket(context.Background(), "")
	if err != nil {
		t.Fatalf("DialPacket: %v", err)
	}
	defer pc.Close()
	_ = pc.SetDeadline(time.Now().Add(2 * time.Second))

	target, err := net.ResolveUDPAddr("udp", net.JoinHostPort(openVPNLifecycleTargetIP, "9001"))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("openvpn udp")
	if _, err := pc.WriteTo(payload, target); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	buf := make([]byte, 1024)
	n, from, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if !bytes.Equal(buf[:n], payload) {
		t.Fatalf("udp echo = %q, want %q", buf[:n], payload)
	}
	if from.String() != target.String() {
		t.Fatalf("from = %s, want %s", from, target)
	}
}

type openVPNLifecycleFactory struct {
	mu      sync.Mutex
	creates map[string]int
	closes  map[string]int
}

func installOpenVPNLifecycleFactory(t *testing.T) *openVPNLifecycleFactory {
	t.Helper()
	f := &openVPNLifecycleFactory{
		creates: map[string]int{},
		closes:  map[string]int{},
	}
	oldFactory := newOpenVPNInstance
	newOpenVPNInstance = f.newInstance
	t.Cleanup(func() { newOpenVPNInstance = oldFactory })
	return f
}

func (f *openVPNLifecycleFactory) newInstance(_ context.Context, cfg *config) (*instance, error) {
	targetIP := netip.MustParseAddr(openVPNLifecycleTargetIP)
	tunDev, tnet, err := netstack.CreateNetTUN([]netip.Addr{targetIP}, nil, cfg.tunMTU)
	if err != nil {
		return nil, err
	}
	ln, err := tnet.ListenTCPAddrPort(netip.AddrPortFrom(targetIP, openVPNLifecycleTargetPort))
	if err != nil {
		_ = tunDev.Close()
		return nil, err
	}
	udp, err := tnet.ListenUDPAddrPort(netip.AddrPortFrom(targetIP, openVPNLifecycleUDPTargetPort))
	if err != nil {
		_ = ln.Close()
		_ = tunDev.Close()
		return nil, err
	}
	go serveOpenVPNLifecycleEcho(ln)
	go serveOpenVPNLifecycleUDPEcho(udp)

	f.mu.Lock()
	f.creates[cfg.remote]++
	f.mu.Unlock()

	bgCtx, cancel := context.WithCancel(context.Background())
	return &instance{
		cfg:       cfg,
		tunDev:    &openVPNLifecycleTun{Device: tunDev, closeFunc: f.closeFunc(cfg.remote, ln, udp, tunDev)},
		tnet:      tnet,
		addresses: []netip.Addr{targetIP},
		ctx:       bgCtx,
		cancel:    cancel,
	}, nil
}

func (f *openVPNLifecycleFactory) closeFunc(remote string, ln, udp openVPNLifecycleCloser, tunDev tun.Device) func() error {
	return func() error {
		f.mu.Lock()
		f.closes[remote]++
		f.mu.Unlock()
		return errors.Join(ln.Close(), udp.Close(), tunDev.Close())
	}
}

func (f *openVPNLifecycleFactory) createCount(remote string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creates[remote]
}

func (f *openVPNLifecycleFactory) closeCount(remote string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes[remote]
}

type openVPNLifecycleTun struct {
	tun.Device
	closeFunc func() error
}

func (d *openVPNLifecycleTun) Close() error {
	return d.closeFunc()
}

type openVPNLifecycleListener interface {
	Accept() (net.Conn, error)
}

type openVPNLifecycleCloser interface {
	Close() error
}

func serveOpenVPNLifecycleEcho(ln openVPNLifecycleListener) {
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

func serveOpenVPNLifecycleUDPEcho(pc net.PacketConn) {
	buf := make([]byte, 2048)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		_, _ = pc.WriteTo(buf[:n], addr)
	}
}

func openVPNLifecycleConfig(t *testing.T, profileName, socksAddr, remote string) *appconfig.Config {
	t.Helper()
	ca, cert, key := testFixturePEMs(t)
	return &appconfig.Config{
		Active: profileName,
		Profiles: []appconfig.Profile{{
			Name: profileName,
			Listen: appconfig.ListenConfig{
				SOCKS5: socksAddr,
			},
			Chains: []appconfig.ChainConfig{{
				Name: "default",
				Servers: []appconfig.ServerConfig{{
					Name:     "openvpn-" + profileName,
					Address:  remote,
					Protocol: "openvpn",
					Settings: map[string]any{
						"ca_cert":     ca,
						"client_cert": cert,
						"client_key":  key,
					},
				}},
			}},
		}},
	}
}

func openVPNLifecycleSOCKSConnectEcho(t *testing.T, socksAddr string, payload []byte) {
	t.Helper()
	c, err := net.DialTimeout("tcp", socksAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial SOCKS listener: %v", err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))

	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(c, method); err != nil {
		t.Fatalf("read method: %v", err)
	}
	if !bytes.Equal(method, []byte{0x05, 0x00}) {
		t.Fatalf("method = %v, want [5 0]", method)
	}

	req := []byte{0x05, 0x01, 0x00, 0x01, 10, 65, 0, 1, 0, 0}
	binary.BigEndian.PutUint16(req[len(req)-2:], openVPNLifecycleTargetPort)
	if _, err := c.Write(req); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatalf("read CONNECT reply: %v", err)
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("CONNECT reply = %v, want success", reply)
	}

	if _, err := c.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echo = %q, want %q", got, payload)
	}
}

func openVPNLifecycleTarget() string {
	return net.JoinHostPort(openVPNLifecycleTargetIP, "9000")
}

func freeOpenVPNLifecycleTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}
