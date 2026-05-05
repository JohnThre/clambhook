package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/clambhook/clambhook/internal/config"
	"github.com/clambhook/clambhook/internal/engine"
)

func TestAuthTokenIsOptionalForLoopbackDefaults(t *testing.T) {
	srv := New(engine.New(testAuthConfig(), nil), nil)

	rec := performStatusRequest(t, srv, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
}

func TestAuthTokenRejectsMissingAndWrongBearer(t *testing.T) {
	srv := NewWithOptions(engine.New(testAuthConfig(), nil), nil, Options{AuthToken: "secret-token"})

	cases := []struct {
		name   string
		header string
	}{
		{name: "missing"},
		{name: "wrong scheme", header: "Basic secret-token"},
		{name: "wrong token", header: "Bearer wrong-token"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := performStatusRequest(t, srv, tc.header)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d body=%q, want 401", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAuthTokenAcceptsMatchingBearer(t *testing.T) {
	srv := NewWithOptions(engine.New(testAuthConfig(), nil), nil, Options{AuthToken: "secret-token"})

	rec := performStatusRequest(t, srv, "Bearer secret-token")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
}

func TestValidateAPIAuthConfigRequiresTokenForNonLoopbackBind(t *testing.T) {
	cases := []struct {
		name  string
		addr  string
		token string
		err   bool
	}{
		{name: "loopback ipv4 without token", addr: "127.0.0.1:9090"},
		{name: "loopback ipv6 without token", addr: "[::1]:9090"},
		{name: "localhost without token", addr: "localhost:9090"},
		{name: "wildcard without token", addr: "0.0.0.0:9090", err: true},
		{name: "empty host without token", addr: ":9090", err: true},
		{name: "lan without token", addr: "192.168.1.10:9090", err: true},
		{name: "lan with token", addr: "192.168.1.10:9090", token: "secret-token"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAuthConfig(tc.addr, tc.token)
			if tc.err && err == nil {
				t.Fatal("ValidateAuthConfig returned nil, want error")
			}
			if !tc.err && err != nil {
				t.Fatalf("ValidateAuthConfig returned error: %v", err)
			}
		})
	}
}

func performStatusRequest(t *testing.T, srv *Server, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	return rec
}

func testAuthConfig() *config.Config {
	return &config.Config{
		Active: "default",
		Profiles: []config.Profile{{
			Name: "default",
			Chains: []config.ChainConfig{{
				Name: "default",
				Servers: []config.ServerConfig{{
					Name:     "test",
					Address:  "127.0.0.1:1",
					Protocol: "shadowsocks",
				}},
			}},
		}},
	}
}
