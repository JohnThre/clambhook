//go:build e2e

package e2e

import (
	"context"
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

	_ "github.com/JohnThre/clambhook/internal/protocol/openvpn"
)

func TestOpenVPNExternalCompatibility(t *testing.T) {
	requireE2E(t)

	remote := os.Getenv("CLAMBHOOK_E2E_OPENVPN_REMOTE")
	caPath := os.Getenv("CLAMBHOOK_E2E_OPENVPN_CA")
	certPath := os.Getenv("CLAMBHOOK_E2E_OPENVPN_CLIENT_CERT")
	keyPath := os.Getenv("CLAMBHOOK_E2E_OPENVPN_CLIENT_KEY")
	tcpTarget := os.Getenv("CLAMBHOOK_E2E_OPENVPN_TCP_TARGET")
	udpTarget := os.Getenv("CLAMBHOOK_E2E_OPENVPN_UDP_TARGET")
	if remote == "" || caPath == "" || certPath == "" || keyPath == "" || tcpTarget == "" || udpTarget == "" {
		skipOrFatal(t, openVPNExternalPrerequisite)
	}

	caPEM := readPEMFile(t, caPath)
	certPEM := readPEMFile(t, certPath)
	keyPEM := readPEMFile(t, keyPath)
	ch := singleNodeChain("openvpn-external", node("openvpn", remote, "openvpn", map[string]any{
		"ca_cert":     caPEM,
		"client_cert": certPEM,
		"client_key":  keyPEM,
	}))
	defer ch.Close()

	// The corrected handshake contract means the OpenVPN hop advertises
	// native UDP forwarding once the control channel is up.
	assertOpenVPNCapabilities(t, ch)

	t.Run("tcp", func(t *testing.T) {
		assertTCPRoundTrip(t, ch, tcpTarget)
	})
	t.Run("udp", func(t *testing.T) {
		assertUDPRoundTrip(t, ch, "", udpTarget)
	})
}

// TestOpenVPNLocalServerHandshake is the strongest self-contained
// integration coverage we can provide without a fake no-op server. It
// starts a real OpenVPN process with dev null (no TUN privileges needed),
// drives the full control-channel handshake against it, and verifies the
// chain reports native UDP capability. If no OpenVPN binary is installed or
// it cannot run in this environment, the test skips with the exact
// prerequisites.
func TestOpenVPNLocalServerHandshake(t *testing.T) {
	requireE2E(t)

	if !commandExists("openvpn") {
		skipOrFatal(t, openVPNExternalPrerequisite)
	}

	dir := t.TempDir()
	caPEM, serverCertPEM, serverKeyPEM, clientCertPEM, clientKeyPEM := writeOpenVPNPKI(t, dir)
	remote, ok := startLocalOpenVPNServer(t, dir, caPEM, serverCertPEM, serverKeyPEM)
	if !ok {
		skipOrFatal(t, "local openvpn server could not start (it must accept dev null without TUN privileges); %s", openVPNExternalPrerequisite)
	}

	ch := singleNodeChain("openvpn-local", node("openvpn", remote, "openvpn", map[string]any{
		"ca_cert":          caPEM,
		"client_cert":      clientCertPEM,
		"client_key":       clientKeyPEM,
		"server_cn":        "localhost",
		"skip_cert_verify": false,
	}))
	defer ch.Close()

	assertOpenVPNCapabilities(t, ch)

	// DialPacket forces the full handshake + netstack bring-up without
	// requiring any tunneled round-trip target. Success here means the
	// corrected 0-based packet IDs and control-channel deadline handling
	// allowed the real server to complete HARD_RESET/TLS/PUSH_REPLY.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pc, err := ch.DialPacket(ctx, "")
	if err != nil {
		t.Fatalf("handshake with local openvpn server failed: %v", err)
	}
	_ = pc.Close()
}

func assertOpenVPNCapabilities(t *testing.T, ch *chain.Chain) {
	t.Helper()
	caps := ch.Capabilities()
	if !caps.UDP {
		t.Fatalf("OpenVPN chain UDP = false, want true")
	}
	if caps.UDPMode != protocol.UDPModeNative {
		t.Fatalf("OpenVPN chain UDPMode = %q, want %q", caps.UDPMode, protocol.UDPModeNative)
	}
	if err := ch.CheckPacketSupport(); err != nil {
		t.Fatalf("OpenVPN CheckPacketSupport: %v", err)
	}
}

func readPEMFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func startLocalOpenVPNServer(t *testing.T, dir, caPEM, serverCertPEM, serverKeyPEM string) (remote string, ok bool) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), []byte(caPEM), 0o600); err != nil {
		t.Fatalf("write ca: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server-cert.pem"), []byte(serverCertPEM), 0o600); err != nil {
		t.Fatalf("write server cert: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server-key.pem"), []byte(serverKeyPEM), 0o600); err != nil {
		t.Fatalf("write server key: %v", err)
	}

	port := mustFreePort(t)
	logPath := filepath.Join(dir, "server.log")
	cfg := fmt.Sprintf(`dev null
mode server
tls-server
topology subnet
server 10.8.0.0 255.255.255.0
port %d
proto udp
ca ca.pem
cert server-cert.pem
key server-key.pem
dh none
data-ciphers AES-256-GCM:CHACHA20-POLY1305
tls-version-min 1.2
explicit-exit-notify 0
push "route-gateway 10.8.0.1"
log-append %s
`, port, logPath)
	cfgPath := filepath.Join(dir, "server.conf")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write server config: %v", err)
	}

	cmd := exec.Command("openvpn", "--config", cfgPath)
	cmd.Dir = dir
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create server log: %v", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		t.Logf("openvpn start failed: %v", err)
		return "", false
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = logFile.Close()
		// Reap the process with a bounded wait so cleanup cannot hang.
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})

	ready := "Initialization Sequence Completed"
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(logPath)
		if err == nil && strings.Contains(string(data), ready) {
			return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), true
		}
		time.Sleep(100 * time.Millisecond)
	}

	data, _ := os.ReadFile(logPath)
	t.Logf("openvpn server did not become ready; log:\n%s", string(data))
	return "", false
}

const openVPNExternalPrerequisite = "set CLAMBHOOK_E2E_OPENVPN_REMOTE, _CA, _CLIENT_CERT, _CLIENT_KEY, _TCP_TARGET, and _UDP_TARGET to run the OpenVPN real-server e2e, or install an OpenVPN binary that accepts dev null for the local-server handshake test"
