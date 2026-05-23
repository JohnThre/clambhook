package wireguard

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
	"golang.zx2c4.com/wireguard/tun/netstack"
)

const (
	wgLifecycleTargetIP   = "10.64.0.1"
	wgLifecycleTargetPort = 9000
)

func TestDialerCloseBeforeFirstDialRejectsFutureDials(t *testing.T) {
	oldFactory := newWireGuardInstance
	factoryCalls := 0
	newWireGuardInstance = func(*config, string) (*wgInstance, error) {
		factoryCalls++
		return nil, errors.New("unexpected factory call")
	}
	t.Cleanup(func() { newWireGuardInstance = oldFactory })

	d := &dialer{server: validServer(), cfg: config{}}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	_, err := d.Dial(context.Background(), "tcp", wgLifecycleTarget())
	if err == nil || !strings.Contains(err.Error(), "dialer closed") {
		t.Fatalf("Dial after Close err = %v, want dialer closed", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls = %d, want 0", factoryCalls)
	}
}

func TestWireGuardInstanceReusedAndClosedOnEngineReload(t *testing.T) {
	factory := installWireGuardLifecycleFactory(t)

	oldAddr := freeWireGuardLifecycleTCPAddr(t)
	newAddr := freeWireGuardLifecycleTCPAddr(t)
	e := engine.New(wireGuardLifecycleConfig("A", oldAddr, "wg-old"), nil)
	t.Cleanup(func() { _ = e.Stop() })

	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	wireGuardLifecycleSOCKSConnectEcho(t, oldAddr, []byte("first"))
	wireGuardLifecycleSOCKSConnectEcho(t, oldAddr, []byte("second"))
	if got := factory.createCount("wg-old"); got != 1 {
		t.Fatalf("old instance creates = %d, want 1", got)
	}

	if err := e.Reload(wireGuardLifecycleConfig("B", newAddr, "wg-new")); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := factory.closeCount("wg-old"); got != 1 {
		t.Fatalf("old instance closes = %d, want 1", got)
	}

	wireGuardLifecycleSOCKSConnectEcho(t, newAddr, []byte("third"))
	if got := factory.createCount("wg-new"); got != 1 {
		t.Fatalf("new instance creates = %d, want 1", got)
	}
	if err := e.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := factory.closeCount("wg-new"); got != 1 {
		t.Fatalf("new instance closes = %d, want 1", got)
	}
}

type wireGuardLifecycleFactory struct {
	mu      sync.Mutex
	creates map[string]int
	closes  map[string]int
}

func installWireGuardLifecycleFactory(t *testing.T) *wireGuardLifecycleFactory {
	t.Helper()
	f := &wireGuardLifecycleFactory{
		creates: map[string]int{},
		closes:  map[string]int{},
	}
	oldFactory := newWireGuardInstance
	newWireGuardInstance = f.newInstance
	t.Cleanup(func() { newWireGuardInstance = oldFactory })
	return f
}

func (f *wireGuardLifecycleFactory) newInstance(cfg *config, serverName string) (*wgInstance, error) {
	targetIP := netip.MustParseAddr(wgLifecycleTargetIP)
	tunDev, tnet, err := netstack.CreateNetTUN([]netip.Addr{targetIP}, nil, cfg.mtu)
	if err != nil {
		return nil, err
	}
	ln, err := tnet.ListenTCPAddrPort(netip.AddrPortFrom(targetIP, wgLifecycleTargetPort))
	if err != nil {
		_ = tunDev.Close()
		return nil, err
	}
	go serveWireGuardLifecycleEcho(ln)

	f.mu.Lock()
	f.creates[serverName]++
	f.mu.Unlock()

	inst := &wgInstance{tnet: tnet, name: serverName}
	inst.closeHook = func() error {
		f.mu.Lock()
		f.closes[serverName]++
		f.mu.Unlock()
		return errors.Join(ln.Close(), tunDev.Close())
	}
	return inst, nil
}

func (f *wireGuardLifecycleFactory) createCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creates[name]
}

func (f *wireGuardLifecycleFactory) closeCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes[name]
}

type wireGuardLifecycleListener interface {
	Accept() (net.Conn, error)
}

func serveWireGuardLifecycleEcho(ln wireGuardLifecycleListener) {
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

func wireGuardLifecycleConfig(profileName, socksAddr, serverName string) *appconfig.Config {
	endpoint := "127.0.0.1:51820"
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
					Name:     serverName,
					Address:  endpoint,
					Protocol: "wireguard",
					Settings: map[string]any{
						"private_key": validKeyB64,
						"addresses":   []any{"10.64.0.2/32"},
						"peers": []map[string]any{{
							"public_key":  validKeyB64,
							"endpoint":    endpoint,
							"allowed_ips": []any{"10.64.0.0/24"},
						}},
					},
				}},
			}},
		}},
	}
}

func wireGuardLifecycleSOCKSConnectEcho(t *testing.T, socksAddr string, payload []byte) {
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

	req := []byte{0x05, 0x01, 0x00, 0x01, 10, 64, 0, 1, 0, 0}
	binary.BigEndian.PutUint16(req[len(req)-2:], wgLifecycleTargetPort)
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

func wgLifecycleTarget() string {
	return net.JoinHostPort(wgLifecycleTargetIP, "9000")
}

func freeWireGuardLifecycleTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}
