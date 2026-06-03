package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
)

func TestDNSEndpointReturnsDisabledRouteStrategy(t *testing.T) {
	srv := New(engine.New(testServersConfig("A"), nil), nil)

	resp := getDNS(t, srv, "/api/v1/dns")

	if resp.Profile != "A" || resp.Strategy != "route" || resp.Enabled || resp.InterceptsPort53 {
		t.Fatalf("dns response = %+v, want disabled route strategy for A", resp)
	}
	if resp.Timeout != "" || len(resp.Upstreams) != 0 {
		t.Fatalf("dns response = %+v, want no timeout/upstreams when disabled", resp)
	}
}

func TestDNSEndpointReturnsEnabledEncryptedStrategyForRequestedProfile(t *testing.T) {
	cfg := testServersConfig("A")
	cfg.Profiles[1].DNS = config.DNSConfig{
		Enabled: true,
		Timeout: config.Duration(3 * time.Second),
		Upstreams: []config.DNSUpstreamConfig{{
			Name:         "cloudflare",
			Protocol:     "doh",
			URL:          "https://cloudflare-dns.com/dns-query",
			ServerName:   "cloudflare-dns.com",
			BootstrapIPs: []string{"1.1.1.1"},
		}},
	}
	srv := New(engine.New(cfg, nil), nil)

	resp := getDNS(t, srv, "/api/v1/dns?profile=B")

	if resp.Profile != "B" || resp.Strategy != "encrypted" || !resp.Enabled || !resp.InterceptsPort53 || resp.Timeout != "3s" {
		t.Fatalf("dns response = %+v, want encrypted profile B strategy", resp)
	}
	if len(resp.Upstreams) != 1 || resp.Upstreams[0].Name != "cloudflare" || resp.Upstreams[0].Protocol != "doh" {
		t.Fatalf("upstreams = %+v, want cloudflare DoH", resp.Upstreams)
	}
}

func TestDNSEndpointUsesDefaultTimeoutWhenEnabled(t *testing.T) {
	cfg := testServersConfig("A")
	cfg.Profiles[0].DNS = config.DNSConfig{
		Enabled: true,
		Upstreams: []config.DNSUpstreamConfig{{
			Protocol: "dot",
			Address:  "1.1.1.1:853",
		}},
	}
	srv := New(engine.New(cfg, nil), nil)

	resp := getDNS(t, srv, "/api/v1/dns")

	if resp.Timeout != "5s" {
		t.Fatalf("timeout = %q, want default 5s", resp.Timeout)
	}
}

func TestDNSEndpointRejectsMissingProfile(t *testing.T) {
	srv := New(engine.New(testServersConfig("A"), nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dns?profile=missing", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%q, want 404", rec.Code, rec.Body.String())
	}
}

type dnsAPIResponse struct {
	Profile          string                     `json:"profile"`
	Strategy         string                     `json:"strategy"`
	Enabled          bool                       `json:"enabled"`
	Timeout          string                     `json:"timeout"`
	Upstreams        []config.DNSUpstreamConfig `json:"upstreams"`
	InterceptsPort53 bool                       `json:"intercepts_port_53"`
}

func getDNS(t *testing.T, srv *Server, path string) dnsAPIResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp dnsAPIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}
