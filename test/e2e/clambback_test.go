//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/JohnThre/clambhook/internal/protocol/clambback"
)

const clambbackPassword = "clambhook-e2e-clambback"

type clambbackFixture struct {
	addr string
}

func TestClambbackCompatibility(t *testing.T) {
	requireE2E(t)

	tcpEcho := startTCPEcho(t)
	udpEcho := startUDPEcho(t)
	fx := startClambbackFixture(t)

	srv := node("clambback", fx.addr, "clambback", map[string]any{
		"password":         clambbackPassword,
		"sni":              "localhost",
		"skip_cert_verify": true,
	})

	t.Run("tcp", func(t *testing.T) {
		ch := singleNodeChain("clambback", srv)
		defer ch.Close()
		assertTCPRoundTrip(t, ch, tcpEcho.addr)
	})

	t.Run("udp", func(t *testing.T) {
		ch := singleNodeChain("clambback", srv)
		defer ch.Close()
		assertUDPRoundTrip(t, ch, "", udpEcho.addr)
	})
}

func TestDaemonSOCKSAgainstClambback(t *testing.T) {
	requireE2E(t)
	bin := os.Getenv("CLAMBHOOK_BIN")
	if bin == "" {
		skipOrFatal(t, "CLAMBHOOK_BIN must point to a built clambhook binary")
	}
	if _, err := os.Stat(bin); err != nil {
		skipOrFatal(t, "CLAMBHOOK_BIN %q is not usable: %v", bin, err)
	}

	tcpEcho := startTCPEcho(t)
	udpEcho := startUDPEcho(t)
	fx := startClambbackFixture(t)

	dir := t.TempDir()
	socksAddr := mustFreeTCPAddr(t)
	apiAddr := mustFreeTCPAddr(t)
	cfgPath := filepath.Join(dir, "clambhook.toml")
	cfg := fmt.Sprintf(`
active = "e2e"

[traffic]
enabled = false

[[profile]]
name = "e2e"

  [profile.listen]
  socks5 = %q

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
`, socksAddr, fx.addr, clambbackPassword)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	startCommand(t, bin, "-config", cfgPath, "-api", apiAddr, "-no-watch")
	if err := waitTCP(socksAddr, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	socks5TCPRoundTrip(t, socksAddr, tcpEcho.addr)
	socks5UDPRoundTrip(t, socksAddr, udpEcho.addr)
}

func startClambbackFixture(t *testing.T) clambbackFixture {
	t.Helper()
	bin := os.Getenv("CLAMBBACK_BIN")
	if bin == "" {
		skipOrFatal(t, "CLAMBBACK_BIN must point to a built clambback binary")
	}
	if _, err := os.Stat(bin); err != nil {
		skipOrFatal(t, "CLAMBBACK_BIN %q is not usable: %v", bin, err)
	}

	dir := t.TempDir()
	certPath, keyPath := writeSelfSignedCert(t, dir)
	port := mustFreePort(t)
	cfgPath := filepath.Join(dir, "server.json")
	cfg := map[string]any{
		"run_type":    "server",
		"local_addr":  "127.0.0.1",
		"local_port":  port,
		"remote_addr": "127.0.0.1",
		"remote_port": 9,
		"password":    []string{clambbackPassword},
		"log_level":   0,
		"ssl": map[string]any{
			"cert":                 certPath,
			"key":                  keyPath,
			"key_password":         "",
			"cipher":               "",
			"cipher_tls13":         "",
			"prefer_server_cipher": true,
			"alpn":                 []string{},
			"alpn_port_override":   map[string]uint16{},
			"reuse_session":        false,
			"session_ticket":       false,
			"session_timeout":      600,
			"plain_http_response":  "",
			"curves":               "",
			"dhparam":              "",
		},
		"tcp": map[string]any{
			"prefer_ipv4":    false,
			"no_delay":       true,
			"keep_alive":     true,
			"reuse_port":     false,
			"fast_open":      false,
			"fast_open_qlen": 20,
		},
		"mysql": map[string]any{"enabled": false},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := startCommand(t, bin, cfgPath, "-l", filepath.Join(dir, "clambback.log"))
	if err := waitTCP(net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)), 5*time.Second); err != nil {
		t.Fatalf("wait clambback: %v\n%s", err, cmd.output.String())
	}
	return clambbackFixture{addr: net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))}
}
