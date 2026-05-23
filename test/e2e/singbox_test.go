//go:build e2e

package e2e

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
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

	_ "github.com/JohnThre/clambhook/internal/protocol/reality"
	_ "github.com/JohnThre/clambhook/internal/protocol/shadowsocks"
	_ "github.com/JohnThre/clambhook/internal/protocol/trojan"
	_ "github.com/JohnThre/clambhook/internal/protocol/vless"
	_ "github.com/JohnThre/clambhook/internal/protocol/vmess"
)

const (
	singBoxImage         = "ghcr.io/sagernet/sing-box:v1.13.12"
	testUUID             = "b831381d-6324-4d53-ad4f-8cda48b30811"
	ssPassword           = "clambhook-e2e-shadowsocks"
	trojanPassword       = "clambhook-e2e-trojan"
	realityPrivateKey    = "GL-gJN9rHYSgOQJmtuUrWxIsRcqxfzSyxff6ZOIua10"
	realityPublicKey     = "_dQMsVgLpU0bJUGfgTfCOgn5BA_sJkJf5hBDZM0h0GI"
	realityShortID       = "0123456789abcdef"
	singBoxContainerWork = "/work"
)

type singBoxFixture struct {
	ssAddr      string
	trojanAddr  string
	vlessAddr   string
	realityAddr string
	vmessAddr   string
}

type singBoxRunner struct {
	backend string
	dir     string
	ports   []int
}

func TestSingBoxProtocolCompatibility(t *testing.T) {
	requireE2E(t)

	tcpEcho := startTCPEcho(t)
	udpEcho := startUDPEcho(t)
	fx := startSingBoxFixture(t)

	cases := []struct {
		name         string
		server       protocol.Server
		udpSession   string
		udpDatagram  string
		udpSupported bool
	}{
		{
			name: "shadowsocks",
			server: node("ss", fx.ssAddr, "shadowsocks", map[string]any{
				"method":   "aes-128-gcm",
				"password": ssPassword,
			}),
			udpDatagram:  udpEcho.addr,
			udpSupported: true,
		},
		{
			name: "trojan",
			server: node("trojan", fx.trojanAddr, "trojan", map[string]any{
				"password":         trojanPassword,
				"sni":              "localhost",
				"skip_cert_verify": true,
			}),
			udpDatagram:  udpEcho.addr,
			udpSupported: true,
		},
		{
			name: "vless_tls",
			server: node("vless", fx.vlessAddr, "vless", map[string]any{
				"uuid":             testUUID,
				"sni":              "localhost",
				"skip_cert_verify": true,
			}),
			udpSession:   udpEcho.addr,
			udpDatagram:  udpEcho.addr,
			udpSupported: true,
		},
		{
			name: "vless_reality",
			server: node("reality", fx.realityAddr, "vless", map[string]any{
				"uuid":     testUUID,
				"security": "reality",
				"reality": map[string]any{
					"public_key":  realityPublicKey,
					"short_id":    realityShortID,
					"server_name": "localhost",
					"fingerprint": "chrome",
				},
			}),
			udpSession:   udpEcho.addr,
			udpDatagram:  udpEcho.addr,
			udpSupported: true,
		},
		{
			name: "vmess_legacy_udp",
			server: node("vmess", fx.vmessAddr, "vmess", map[string]any{
				"uuid":             testUUID,
				"security":         "aes-128-gcm",
				"sni":              "localhost",
				"skip_cert_verify": true,
				"packet_encoding":  "legacy",
			}),
			udpSession:   udpEcho.addr,
			udpDatagram:  udpEcho.addr,
			udpSupported: true,
		},
		{
			name: "vmess_xudp",
			server: node("vmess-xudp", fx.vmessAddr, "vmess", map[string]any{
				"uuid":             testUUID,
				"security":         "aes-128-gcm",
				"sni":              "localhost",
				"skip_cert_verify": true,
				"packet_encoding":  "xudp",
			}),
			udpDatagram:  udpEcho.addr,
			udpSupported: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/tcp", func(t *testing.T) {
			assertTCPRoundTrip(t, singleNodeChain(tc.name, tc.server), tcpEcho.addr)
		})
		if tc.udpSupported {
			t.Run(tc.name+"/udp", func(t *testing.T) {
				assertUDPRoundTrip(t, singleNodeChain(tc.name, tc.server), tc.udpSession, tc.udpDatagram)
			})
		}
	}
}

func TestSingBoxChainCompatibility(t *testing.T) {
	requireE2E(t)

	tcpEcho := startTCPEcho(t)
	udpEcho := startUDPEcho(t)
	fx := startSingBoxFixture(t)

	ss := node("ss-entry", fx.ssAddr, "shadowsocks", map[string]any{
		"method":   "aes-128-gcm",
		"password": ssPassword,
	})
	trojan := node("trojan-final", fx.trojanAddr, "trojan", map[string]any{
		"password":         trojanPassword,
		"sni":              "localhost",
		"skip_cert_verify": true,
	})
	vmessXUDP := node("vmess-xudp-final", fx.vmessAddr, "vmess", map[string]any{
		"uuid":             testUUID,
		"security":         "aes-128-gcm",
		"sni":              "localhost",
		"skip_cert_verify": true,
		"packet_encoding":  "xudp",
	})
	reality := node("reality-entry", fx.realityAddr, "vless", map[string]any{
		"uuid":     testUUID,
		"security": "reality",
		"reality": map[string]any{
			"public_key":  realityPublicKey,
			"short_id":    realityShortID,
			"server_name": "localhost",
			"fingerprint": "chrome",
		},
	})

	t.Run("tcp_shadowsocks_to_trojan", func(t *testing.T) {
		ch := &chain.Chain{Name: "ss-to-trojan", Nodes: []protocol.Server{ss, trojan}}
		defer ch.Close()
		assertTCPRoundTrip(t, ch, tcpEcho.addr)
	})

	t.Run("tcp_reality_to_vmess", func(t *testing.T) {
		ch := &chain.Chain{Name: "reality-to-vmess", Nodes: []protocol.Server{reality, vmessXUDP}}
		defer ch.Close()
		assertTCPRoundTrip(t, ch, tcpEcho.addr)
	})

	t.Run("udp_shadowsocks_to_trojan", func(t *testing.T) {
		ch := &chain.Chain{Name: "ss-to-trojan-udp", Nodes: []protocol.Server{ss, trojan}}
		defer ch.Close()
		assertUDPRoundTrip(t, ch, "", udpEcho.addr)
	})

	t.Run("udp_trojan_to_vmess_xudp", func(t *testing.T) {
		ch := &chain.Chain{Name: "trojan-to-vmess-xudp", Nodes: []protocol.Server{trojan, vmessXUDP}}
		defer ch.Close()
		assertUDPRoundTrip(t, ch, "", udpEcho.addr)
	})

	t.Run("negative_shadowsocks_final_udp_over_stream", func(t *testing.T) {
		ch := &chain.Chain{Name: "trojan-to-ss-udp", Nodes: []protocol.Server{trojan, ss}}
		defer ch.Close()
		_, err := ch.DialPacket(context.Background(), udpEcho.addr)
		if err == nil || !strings.Contains(err.Error(), "Shadowsocks") && !strings.Contains(err.Error(), "shadowsocks") {
			t.Fatalf("DialPacket err = %v, want shadowsocks stream-UDP unsupported", err)
		}
	})
}

func startSingBoxFixture(t *testing.T) singBoxFixture {
	t.Helper()
	dir := t.TempDir()
	runner := newSingBoxRunner(t, dir)

	ports := map[string]int{
		"ss":      mustFreePort(t),
		"trojan":  mustFreePort(t),
		"vless":   mustFreePort(t),
		"reality": mustFreePort(t),
		"vmess":   mustFreePort(t),
		"decoy":   mustFreePort(t),
	}
	for _, p := range ports {
		runner.ports = append(runner.ports, p)
	}

	certPath, keyPath := writeSelfSignedCert(t, dir)
	listenHost := "127.0.0.1"
	decoyHost := "localhost"
	if runner.backend == "docker" {
		listenHost = "0.0.0.0"
		decoyHost = "host.docker.internal"
		certPath = filepath.Join(singBoxContainerWork, filepath.Base(certPath))
		keyPath = filepath.Join(singBoxContainerWork, filepath.Base(keyPath))
	}
	startTLSDecoy(t, ports["decoy"], certPathForLocal(t, dir, "server-cert.pem"), certPathForLocal(t, dir, "server-key.pem"))

	cfg := map[string]any{
		"log": map[string]any{"level": "warn", "timestamp": true},
		"inbounds": []any{
			map[string]any{
				"type":        "shadowsocks",
				"tag":         "ss-in",
				"listen":      listenHost,
				"listen_port": ports["ss"],
				"method":      "aes-128-gcm",
				"password":    ssPassword,
			},
			map[string]any{
				"type":        "trojan",
				"tag":         "trojan-in",
				"listen":      listenHost,
				"listen_port": ports["trojan"],
				"users": []any{map[string]any{
					"name":     "clambhook",
					"password": trojanPassword,
				}},
				"tls": map[string]any{
					"enabled":          true,
					"server_name":      "localhost",
					"certificate_path": certPath,
					"key_path":         keyPath,
				},
			},
			map[string]any{
				"type":        "vless",
				"tag":         "vless-in",
				"listen":      listenHost,
				"listen_port": ports["vless"],
				"users": []any{map[string]any{
					"name": "clambhook",
					"uuid": testUUID,
					"flow": "",
				}},
				"tls": map[string]any{
					"enabled":          true,
					"server_name":      "localhost",
					"certificate_path": certPath,
					"key_path":         keyPath,
				},
			},
			map[string]any{
				"type":        "vless",
				"tag":         "reality-in",
				"listen":      listenHost,
				"listen_port": ports["reality"],
				"users": []any{map[string]any{
					"name": "clambhook",
					"uuid": testUUID,
					"flow": "",
				}},
				"tls": map[string]any{
					"enabled":     true,
					"server_name": "localhost",
					"reality": map[string]any{
						"enabled": true,
						"handshake": map[string]any{
							"server":      decoyHost,
							"server_port": ports["decoy"],
						},
						"private_key": realityPrivateKey,
						"short_id":    []string{realityShortID},
					},
				},
			},
			map[string]any{
				"type":        "vmess",
				"tag":         "vmess-in",
				"listen":      listenHost,
				"listen_port": ports["vmess"],
				"users": []any{map[string]any{
					"name":    "clambhook",
					"uuid":    testUUID,
					"alterId": 0,
				}},
				"tls": map[string]any{
					"enabled":          true,
					"server_name":      "localhost",
					"certificate_path": certPath,
					"key_path":         keyPath,
				},
			},
		},
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
		"route":     map[string]any{"final": "direct"},
	}

	configPath := filepath.Join(dir, "sing-box.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	runner.check(t, configPath)
	runner.start(t, configPath)

	for _, p := range []int{ports["ss"], ports["trojan"], ports["vless"], ports["reality"], ports["vmess"]} {
		if err := waitTCP(net.JoinHostPort("127.0.0.1", strconv.Itoa(p)), 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}

	return singBoxFixture{
		ssAddr:      net.JoinHostPort("127.0.0.1", strconv.Itoa(ports["ss"])),
		trojanAddr:  net.JoinHostPort("127.0.0.1", strconv.Itoa(ports["trojan"])),
		vlessAddr:   net.JoinHostPort("127.0.0.1", strconv.Itoa(ports["vless"])),
		realityAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(ports["reality"])),
		vmessAddr:   net.JoinHostPort("127.0.0.1", strconv.Itoa(ports["vmess"])),
	}
}

func newSingBoxRunner(t *testing.T, dir string) *singBoxRunner {
	t.Helper()
	backend := os.Getenv("CLAMBHOOK_E2E_BACKEND")
	if backend == "" {
		backend = "auto"
	}
	switch backend {
	case "auto":
		if commandExists("sing-box") {
			return &singBoxRunner{backend: "local", dir: dir}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if dockerUsable(ctx) {
			return &singBoxRunner{backend: "docker", dir: dir}
		}
		skipOrFatal(t, "sing-box is not on PATH and Docker is unavailable")
	case "local":
		if !commandExists("sing-box") {
			skipOrFatal(t, "sing-box is not on PATH")
		}
		return &singBoxRunner{backend: "local", dir: dir}
	case "docker":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !dockerUsable(ctx) {
			skipOrFatal(t, "Docker is unavailable")
		}
		return &singBoxRunner{backend: "docker", dir: dir}
	default:
		t.Fatalf("unsupported CLAMBHOOK_E2E_BACKEND=%q", backend)
	}
	panic("unreachable")
}

func (r *singBoxRunner) check(t *testing.T, configPath string) {
	t.Helper()
	var cmd *exec.Cmd
	if r.backend == "local" {
		cmd = exec.Command("sing-box", "check", "-c", configPath)
	} else {
		cmd = exec.Command("docker", "run", "--rm",
			"-v", r.dir+":"+singBoxContainerWork,
			"-w", singBoxContainerWork,
			singBoxImage, "check", "-c", filepath.Base(configPath))
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sing-box check: %v\n%s", err, out)
	}
}

func (r *singBoxRunner) start(t *testing.T, configPath string) {
	t.Helper()
	if r.backend == "local" {
		startCommand(t, "sing-box", "run", "-c", configPath)
		return
	}
	args := []string{"run", "--rm", "--add-host", "host.docker.internal:host-gateway", "-v", r.dir + ":" + singBoxContainerWork, "-w", singBoxContainerWork}
	for _, p := range r.ports {
		spec := fmt.Sprintf("127.0.0.1:%d:%d", p, p)
		args = append(args, "-p", spec+"/tcp", "-p", spec+"/udp")
	}
	args = append(args, singBoxImage, "run", "-c", filepath.Base(configPath))
	startCommand(t, "docker", args...)
}

func startTLSDecoy(t *testing.T, port int, certPath, keyPath string) {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = ioCopyDiscard(c)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})
}

func ioCopyDiscard(c net.Conn) (int64, error) {
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	return io.Copy(io.Discard, c)
}

func certPathForLocal(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	return path
}
