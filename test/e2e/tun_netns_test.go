//go:build e2e && linux

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const (
	tunE2EHostAddress   = "10.203.0.1"
	tunE2ENetNSAddress  = "10.203.0.2"
	tunE2ETargetAddress = "198.18.0.1"
)

// TestDaemonTUNNetNSRoundTrip is the privileged black-box TUN contract. It
// starts the real daemon in a Linux network namespace, sends a TCP packet into
// the daemon-owned kernel TUN interface, traverses first-party ClambBack, and
// verifies the echo response returned to the namespace client.
func TestDaemonTUNNetNSRoundTrip(t *testing.T) {
	requireE2E(t)
	if os.Getenv("CLAMBHOOK_E2E_TUN") != "1" {
		t.Skip("run make e2e-tun to enable privileged TUN coverage")
	}
	if os.Geteuid() != 0 {
		t.Fatal("TUN/netns e2e requires root")
	}
	for _, command := range []string{"ip", "python3"} {
		if !commandExists(command) {
			t.Fatalf("TUN/netns e2e requires %s", command)
		}
	}

	daemonBin := requiredExecutable(t, "CLAMBHOOK_BIN")
	clambbackBin := requiredExecutable(t, "CLAMBBACK_BIN")

	suffix := strconv.Itoa(os.Getpid() % 100000)
	nsName := "che2e-" + suffix
	hostVeth := "chh" + suffix
	nsVeth := "chn" + suffix
	tunName := "cht" + suffix

	runE2ECommand(t, "ip", "netns", "add", nsName)
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", nsName).Run() })
	runE2ECommand(t, "ip", "link", "add", hostVeth, "type", "veth", "peer", "name", nsVeth)
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", hostVeth).Run() })
	runE2ECommand(t, "ip", "link", "set", nsVeth, "netns", nsName)
	runE2ECommand(t, "ip", "addr", "add", tunE2EHostAddress+"/30", "dev", hostVeth)
	runE2ECommand(t, "ip", "addr", "add", tunE2ETargetAddress+"/32", "dev", hostVeth)
	runE2ECommand(t, "ip", "link", "set", hostVeth, "up")
	runE2ECommand(t, "ip", "netns", "exec", nsName, "ip", "link", "set", "lo", "up")
	runE2ECommand(t, "ip", "netns", "exec", nsName, "ip", "addr", "add", tunE2ENetNSAddress+"/30", "dev", nsVeth)
	runE2ECommand(t, "ip", "netns", "exec", nsName, "ip", "link", "set", nsVeth, "up")

	echoAddr := startTUNTCPServer(t)
	clambbackAddr := startTUNClambback(t, clambbackBin)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "clambhook-tun.toml")
	apiPort := mustFreePort(t)
	config := fmt.Sprintf(`active = "e2e"

[traffic]
enabled = false

[[profile]]
name = "e2e"

  [profile.listen.tun]
  enabled = true
  name = %q
  chain = "clambback"
  mtu = 1400
  addresses = ["172.30.255.1/30"]
  routes = ["%s/32"]

  [[profile.chain]]
  name = "clambback"

    [[profile.chain.server]]
    name = "clambback"
    address = %q
    protocol = "clambback"

      [profile.chain.server.settings]
      password = %q
      sni = "localhost"
      skip_cert_verify = true
`, tunName, tunE2ETargetAddress, clambbackAddr, clambbackPassword)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	daemon := startCommand(t, "ip", "netns", "exec", nsName, daemonBin,
		"-config", configPath,
		"-api", net.JoinHostPort("127.0.0.1", strconv.Itoa(apiPort)),
		"-no-watch",
	)
	waitForNetNSInterface(t, nsName, tunName, daemon)

	const payload = "clambhook-tun-netns-roundtrip"
	python := fmt.Sprintf(`import socket
s = socket.create_connection((%q, %d), timeout=10)
s.sendall(%q)
data = b""
while len(data) < %d:
    chunk = s.recv(%d - len(data))
    if not chunk:
        break
    data += chunk
s.close()
assert data == %q, (data, %q)
`, tunE2ETargetAddress, echoAddr.Port, payload, len(payload), len(payload), payload, payload)
	runE2ECommand(t, "ip", "netns", "exec", nsName, "python3", "-c", python)
}

func requiredExecutable(t *testing.T, envName string) string {
	t.Helper()
	path := os.Getenv(envName)
	if path == "" {
		t.Fatalf("%s must point to an executable", envName)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("%s %q is unusable: %v", envName, path, err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatalf("%s %q is not executable", envName, path)
	}
	return path
}

func runE2ECommand(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func waitForNetNSInterface(t *testing.T, namespace, interfaceName string, daemon *managedCmd) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if exec.Command("ip", "netns", "exec", namespace, "ip", "link", "show", "dev", interfaceName).Run() == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("daemon did not create TUN interface %s in namespace %s\n%s", interfaceName, namespace, daemon.output.String())
}

func startTUNTCPServer(t *testing.T) *net.TCPAddr {
	t.Helper()
	ln, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.ParseIP(tunE2ETargetAddress), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})
	return ln.Addr().(*net.TCPAddr)
}

func startTUNClambback(t *testing.T, bin string) string {
	t.Helper()
	dir := t.TempDir()
	certPath, keyPath := writeSelfSignedCert(t, dir)
	port := mustFreePort(t)
	configPath := filepath.Join(dir, "clambback-server.json")
	config := map[string]any{
		"run_type":    "server",
		"local_addr":  tunE2EHostAddress,
		"local_port":  port,
		"remote_addr": "127.0.0.1",
		"remote_port": 9,
		"password":    []string{clambbackPassword},
		"log_level":   0,
		"ssl": map[string]any{
			"cert": certPath, "key": keyPath, "key_password": "",
			"cipher": "", "cipher_tls13": "", "prefer_server_cipher": true,
			"alpn": []string{}, "alpn_port_override": map[string]uint16{},
			"reuse_session": false, "session_ticket": false, "session_timeout": 600,
			"plain_http_response": "", "curves": "", "dhparam": "",
		},
		"tcp": map[string]any{
			"prefer_ipv4": false, "no_delay": true, "keep_alive": true,
			"reuse_port": false, "fast_open": false, "fast_open_qlen": 20,
		},
		"mysql": map[string]any{"enabled": false},
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := startCommand(t, bin, configPath, "-l", filepath.Join(dir, "clambback.log"))
	address := net.JoinHostPort(tunE2EHostAddress, strconv.Itoa(port))
	if err := waitTCP(address, 5*time.Second); err != nil {
		t.Fatalf("wait clambback: %v\n%s", err, cmd.output.String())
	}
	return address
}
