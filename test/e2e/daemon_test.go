//go:build e2e

package e2e

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDaemonSOCKSAgainstRealTrojan(t *testing.T) {
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
	fx := startSingBoxFixture(t)

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
  name = "trojan"

    [[profile.chain.server]]
    name = "trojan"
    address = %q
    protocol = "trojan"

      [profile.chain.server.settings]
      password = %q
      sni = "localhost"
      skip_cert_verify = true
`, socksAddr, fx.trojanAddr, trojanPassword)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	startCommand(t, bin, "-config", cfgPath, "-api", apiAddr, "-no-watch")
	if err := waitTCP(socksAddr, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	socks5TCPRoundTrip(t, socksAddr, tcpEcho.addr)
	socks5UDPRoundTrip(t, socksAddr, udpEcho.addr)

	// Tor-style TCP-only chains are covered separately; this assertion keeps
	// the daemon path honest that the test really went through SOCKS.
	if host, _, err := net.SplitHostPort(socksAddr); err != nil || host != "127.0.0.1" {
		t.Fatalf("unexpected SOCKS address %q", socksAddr)
	}
}
