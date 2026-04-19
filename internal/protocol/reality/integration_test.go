//go:build reality_integration

// Integration test for Reality, gated behind a build tag because it
// needs a real xray-core Reality server to point at. The unit tests in
// session_test.go / verify_test.go / handshake_test.go cover the parts
// we can verify in-process.
//
// To run:
//
//	export REALITY_TEST_SERVER=host:port
//	export REALITY_TEST_PUBKEY=<server X25519 pub, hex or base64-url>
//	export REALITY_TEST_SNI=www.microsoft.com            # optional, defaults to server host
//	export REALITY_TEST_SHORT_ID=a1b2c3d4                # optional, default empty
//	export REALITY_TEST_FINGERPRINT=chrome               # optional, default chrome
//	go test -tags reality_integration -run Integration ./internal/protocol/reality/...
//
// A successful completion of reality.Client against a live server means
// auth_key derivation, AEAD stuffing, and HMAC-SHA512 cert verification
// all matched the server's implementation.
package reality

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/clambhook/clambhook/internal/protocol"
)

func TestIntegration_RealServerDial(t *testing.T) {
	addr := os.Getenv("REALITY_TEST_SERVER")
	pubkey := os.Getenv("REALITY_TEST_PUBKEY")
	if addr == "" || pubkey == "" {
		t.Skip("set REALITY_TEST_SERVER and REALITY_TEST_PUBKEY to run")
	}

	settings := map[string]any{
		"public_key":  pubkey,
		"server_name": os.Getenv("REALITY_TEST_SNI"),
		"short_id":    os.Getenv("REALITY_TEST_SHORT_ID"),
		"fingerprint": os.Getenv("REALITY_TEST_FINGERPRINT"),
	}
	opts, err := ParseOptions(protocol.Server{Address: addr, Settings: settings})
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		t.Fatalf("tcp dial: %v", err)
	}
	defer raw.Close()

	inner, err := Client(ctx, raw, opts)
	if err != nil {
		t.Fatalf("reality.Client: %v", err)
	}
	defer inner.Close()

	// If we're here, the Reality handshake completed and the server
	// proved it knew our auth_key. That's the whole contract.
}
