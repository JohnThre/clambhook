//go:build e2e

// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/protocol"

	_ "github.com/JohnThre/clambhook/internal/protocol/shadowsocks"
	_ "github.com/JohnThre/clambhook/internal/protocol/shadowtls"
)

const (
	shadowTLSPassword   = "clambhook-e2e-shadowtls"
	shadowTLSSSPassword = "clambhook-e2e-shadowtls-ss"
)

// TestSingBoxShadowTLSChainCompatibility verifies clambhook's ShadowTLS v3
// client interoperates with sing-box's ShadowTLS inbound: a
// [shadowtls -> shadowsocks] chain performs the real TLS 1.3 handshake, passes
// the session-id HMAC authentication, and round-trips TCP through the inner
// Shadowsocks exit.
func TestSingBoxShadowTLSChainCompatibility(t *testing.T) {
	requireE2E(t)

	tcpEcho := startTCPEcho(t)
	fx := startShadowTLSFixture(t)

	stls := node("stls-entry", fx.addr, "shadowtls", map[string]any{
		"password":         shadowTLSPassword,
		"sni":              "handshake.local",
		"skip_cert_verify": true,
	})
	ss := node("ss-exit", fx.addr, "shadowsocks", map[string]any{
		"method":   "aes-128-gcm",
		"password": shadowTLSSSPassword,
	})

	ch := &chain.Chain{Name: "shadowtls-ss", Nodes: []protocol.Server{stls, ss}}
	defer ch.Close()
	assertTCPRoundTrip(t, ch, tcpEcho.addr)
}

type shadowTLSFixture struct {
	addr string
}

func startShadowTLSFixture(t *testing.T) shadowTLSFixture {
	t.Helper()
	dir := t.TempDir()
	runner := newSingBoxRunner(t, dir)

	stlsPort := mustFreePort(t)
	runner.ports = append(runner.ports, stlsPort)

	// A genuine TLS 1.3 handshake server that ShadowTLS relays the client's
	// handshake to. Runs on the host; sing-box connects to it as its handshake
	// target.
	hsAddr := startTLS13HandshakeServer(t)
	hsHost, hsPortStr, err := net.SplitHostPort(hsAddr)
	if err != nil {
		t.Fatal(err)
	}
	hsPort, _ := strconv.Atoi(hsPortStr)

	listenHost := "127.0.0.1"
	if runner.backend == "docker" {
		listenHost = "0.0.0.0"
		// Reach the host-side handshake server from inside the container.
		hsHost = "host.docker.internal"
	}

	cfg := map[string]any{
		"log": map[string]any{"level": "warn", "timestamp": true},
		"inbounds": []any{
			map[string]any{
				"type":        "shadowtls",
				"tag":         "stls-in",
				"listen":      listenHost,
				"listen_port": stlsPort,
				"version":     3,
				"strict_mode": true,
				"users": []any{map[string]any{
					"name":     "clambhook",
					"password": shadowTLSPassword,
				}},
				"handshake": map[string]any{
					"server":      hsHost,
					"server_port": hsPort,
				},
				"detour": "ss-in",
			},
			map[string]any{
				"type":     "shadowsocks",
				"tag":      "ss-in",
				"method":   "aes-128-gcm",
				"password": shadowTLSSSPassword,
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

	if err := waitTCP(net.JoinHostPort("127.0.0.1", strconv.Itoa(stlsPort)), 5*time.Second); err != nil {
		t.Fatal(err)
	}

	return shadowTLSFixture{addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(stlsPort))}
}

// startTLS13HandshakeServer runs a minimal TLS 1.3 server that completes
// handshakes and drains any bytes. It stands in for the "handshake server" a
// real ShadowTLS deployment fronts (e.g. a real website).
func startTLS13HandshakeServer(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "handshake.local"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"handshake.local"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
	}

	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				tc := tls.Server(conn, tlsCfg)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := tc.HandshakeContext(ctx); err != nil {
					_ = conn.Close()
					return
				}
				_, _ = io.Copy(io.Discard, tc)
				_ = tc.Close()
			}()
		}
	}()

	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return net.JoinHostPort("127.0.0.1", port)
}
