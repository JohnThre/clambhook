//go:build e2e

package e2e

import (
	"os"
	"testing"

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
		skipOrFatal(t, "set CLAMBHOOK_E2E_OPENVPN_REMOTE, _CA, _CLIENT_CERT, _CLIENT_KEY, _TCP_TARGET, and _UDP_TARGET to run OpenVPN real-server e2e")
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

	t.Run("tcp", func(t *testing.T) {
		assertTCPRoundTrip(t, ch, tcpTarget)
	})
	t.Run("udp", func(t *testing.T) {
		assertUDPRoundTrip(t, ch, "", udpTarget)
	})
}

func readPEMFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
