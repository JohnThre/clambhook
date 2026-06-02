package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	_ "github.com/JohnThre/clambhook/internal/protocol/trojan"
)

type serversResponse struct {
	Profile string `json:"profile"`
	Chains  []struct {
		Name         string `json:"name"`
		HopCount     int    `json:"hop_count"`
		Capabilities struct {
			TCP     bool   `json:"tcp"`
			UDP     bool   `json:"udp"`
			UDPMode string `json:"udp_mode"`
		} `json:"capabilities"`
		Servers []struct {
			Name         string `json:"name"`
			Address      string `json:"address"`
			Protocol     string `json:"protocol"`
			Capabilities struct {
				TCP     bool   `json:"tcp"`
				UDP     bool   `json:"udp"`
				UDPMode string `json:"udp_mode"`
			} `json:"capabilities"`
			Geo struct {
				Country     string `json:"country,omitempty"`
				CountryCode string `json:"country_code,omitempty"`
				City        string `json:"city,omitempty"`
			} `json:"geo,omitempty"`
			GeoError string `json:"geo_error,omitempty"`
		} `json:"servers"`
	} `json:"chains"`
}

func TestServersEndpointReturnsActiveProfileWithGeo(t *testing.T) {
	cfg := testServersConfig("B")
	cfg.Geo.Database = filepath.Join("..", "geo", "testdata", "GeoIP2-City-Test.mmdb")
	srv := New(engine.New(cfg, nil), nil)

	resp := getServers(t, srv)

	if resp.Profile != "B" {
		t.Fatalf("profile = %q, want B", resp.Profile)
	}
	if len(resp.Chains) != 1 {
		t.Fatalf("chains = %d, want 1", len(resp.Chains))
	}
	if resp.Chains[0].Name != "b-default" {
		t.Fatalf("chain name = %q, want b-default", resp.Chains[0].Name)
	}
	if resp.Chains[0].HopCount != 1 || !resp.Chains[0].Capabilities.UDP || resp.Chains[0].Capabilities.UDPMode != "stream" {
		t.Fatalf("chain capabilities = %+v hop_count=%d, want one-hop UDP stream", resp.Chains[0].Capabilities, resp.Chains[0].HopCount)
	}
	if len(resp.Chains[0].Servers) != 1 {
		t.Fatalf("servers = %d, want 1", len(resp.Chains[0].Servers))
	}
	row := resp.Chains[0].Servers[0]
	if row.Name != "london" || row.Address != "81.2.69.142:443" || row.Protocol != "trojan" {
		t.Fatalf("server row = %+v", row)
	}
	if !row.Capabilities.TCP || !row.Capabilities.UDP || row.Capabilities.UDPMode != "stream" {
		t.Fatalf("server capabilities = %+v, want TCP and stream UDP", row.Capabilities)
	}
	if row.Geo.CountryCode != "GB" || row.Geo.Country != "United Kingdom" || row.Geo.City != "London" {
		t.Fatalf("geo = %+v, want GB/United Kingdom/London", row.Geo)
	}
	if row.GeoError != "" {
		t.Fatalf("geo_error = %q, want empty", row.GeoError)
	}
}

func TestServersEndpointReturnsRowLevelGeoError(t *testing.T) {
	cfg := testServersConfig("B")
	cfg.Geo.Database = filepath.Join("..", "geo", "testdata", "GeoIP2-City-Test.mmdb")
	cfg.Profiles[1].Chains[0].Servers[0].Address = ""
	srv := New(engine.New(cfg, nil), nil)

	resp := getServers(t, srv)

	row := resp.Chains[0].Servers[0]
	if row.GeoError == "" {
		t.Fatalf("geo_error empty, want row-level lookup error")
	}
	if !strings.Contains(row.GeoError, "empty address") {
		t.Fatalf("geo_error = %q, want empty address", row.GeoError)
	}
}

func TestServersEndpointReflectsProfileSwitch(t *testing.T) {
	eng := engine.New(testServersConfig("A"), nil)
	srv := New(eng, nil)

	resp := getServers(t, srv)
	if resp.Profile != "A" || resp.Chains[0].Name != "a-default" {
		t.Fatalf("initial inventory = %+v, want profile A chain a-default", resp)
	}

	if err := eng.SetActiveProfile("B"); err != nil {
		t.Fatalf("SetActiveProfile: %v", err)
	}

	resp = getServers(t, srv)
	if resp.Profile != "B" || resp.Chains[0].Name != "b-default" {
		t.Fatalf("switched inventory = %+v, want profile B chain b-default", resp)
	}
}

func getServers(t *testing.T, srv *Server) serversResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp serversResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func testServersConfig(active string) *config.Config {
	return &config.Config{
		Active: active,
		Profiles: []config.Profile{
			{
				Name: "A",
				Chains: []config.ChainConfig{{
					Name: "a-default",
					Servers: []config.ServerConfig{{
						Name:     "a-server",
						Address:  "203.0.113.1:443",
						Protocol: "shadowsocks",
					}},
				}},
			},
			{
				Name: "B",
				Chains: []config.ChainConfig{{
					Name: "b-default",
					Servers: []config.ServerConfig{{
						Name:     "london",
						Address:  "81.2.69.142:443",
						Protocol: "trojan",
						Settings: map[string]any{"password": "secret"},
					}},
				}},
			},
		},
	}
}
