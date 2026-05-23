//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/protocol"

	_ "github.com/JohnThre/clambhook/internal/protocol/tor"
)

func TestTorOnionCompatibility(t *testing.T) {
	requireE2E(t)
	if !commandExists("tor") {
		skipOrFatal(t, "tor is not on PATH")
	}

	echo := startTCPEcho(t)
	dir := t.TempDir()
	socksAddr := mustFreeTCPAddr(t)
	hsDir := filepath.Join(dir, "hidden-service")
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(hsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	torrc := filepath.Join(dir, "torrc")
	cfg := fmt.Sprintf(`
SocksPort %s
DataDirectory %s
HiddenServiceDir %s
HiddenServiceVersion 3
HiddenServicePort 80 %s
Log notice stdout
`, socksAddr, dataDir, hsDir, echo.addr)
	if err := os.WriteFile(torrc, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	startCommand(t, "tor", "-f", torrc)
	if err := waitTCP(socksAddr, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	hostFile := filepath.Join(hsDir, "hostname")
	onion := waitFileText(t, hostFile, 30*time.Second)
	if !strings.HasSuffix(onion, ".onion") {
		t.Fatalf("hidden service hostname = %q", onion)
	}

	torNode := node("tor", socksAddr, "tor", nil)
	ch := singleNodeChain("tor-onion", torNode)
	defer ch.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := ch.Dial(ctx, "tcp", net.JoinHostPort(onion, "80"))
	if err != nil {
		t.Fatalf("tor onion dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	payload := []byte("clambhook-e2e-tor")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("tor write: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("tor read: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("tor echo = %q, want %q", got, payload)
	}

	if err := ch.CheckPacketSupport(); err == nil || !strings.Contains(err.Error(), "does not support UDP") {
		t.Fatalf("Tor UDP support err = %v, want unsupported", err)
	}
}

func TestTorUDPUnsupportedWithoutDaemon(t *testing.T) {
	requireE2E(t)
	ch := &chain.Chain{
		Name: "tor-udp-unsupported",
		Nodes: []protocol.Server{
			node("tor", "127.0.0.1:9050", "tor", nil),
		},
	}
	defer ch.Close()
	if err := ch.CheckPacketSupport(); err == nil || !strings.Contains(err.Error(), "does not support UDP") {
		t.Fatalf("Tor UDP support err = %v, want unsupported", err)
	}
}

func waitFileText(t *testing.T, path string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && len(strings.TrimSpace(string(data))) > 0 {
			return strings.TrimSpace(string(data))
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
	return ""
}
