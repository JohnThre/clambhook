//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
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

	_ "github.com/JohnThre/clambhook/internal/protocol/shadowsocks"
	_ "github.com/JohnThre/clambhook/internal/protocol/trojan"
)

const (
	singBoxImage         = "ghcr.io/sagernet/sing-box:v1.13.12"
	ssPassword           = "clambhook-e2e-shadowsocks"
	trojanPassword       = "clambhook-e2e-trojan"
	singBoxContainerWork = "/work"
)

type singBoxFixture struct {
	ssAddr     string
	trojanAddr string
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

	t.Run("tcp_shadowsocks_to_trojan", func(t *testing.T) {
		ch := &chain.Chain{Name: "ss-to-trojan", Nodes: []protocol.Server{ss, trojan}}
		defer ch.Close()
		assertTCPRoundTrip(t, ch, tcpEcho.addr)
	})

	t.Run("udp_shadowsocks_to_trojan", func(t *testing.T) {
		ch := &chain.Chain{Name: "ss-to-trojan-udp", Nodes: []protocol.Server{ss, trojan}}
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
		"ss":     mustFreePort(t),
		"trojan": mustFreePort(t),
	}
	for _, p := range ports {
		runner.ports = append(runner.ports, p)
	}

	certPath, keyPath := writeSelfSignedCert(t, dir)
	listenHost := "127.0.0.1"
	if runner.backend == "docker" {
		listenHost = "0.0.0.0"
		certPath = filepath.Join(singBoxContainerWork, filepath.Base(certPath))
		keyPath = filepath.Join(singBoxContainerWork, filepath.Base(keyPath))
	}

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

	for _, p := range []int{ports["ss"], ports["trojan"]} {
		if err := waitTCP(net.JoinHostPort("127.0.0.1", strconv.Itoa(p)), 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}

	return singBoxFixture{
		ssAddr:     net.JoinHostPort("127.0.0.1", strconv.Itoa(ports["ss"])),
		trojanAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(ports["trojan"])),
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
