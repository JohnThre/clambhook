package api

import (
	"net/http"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
)

const defaultDNSProxyTimeout = 5 * time.Second

type dnsPayload struct {
	Profile          string                     `json:"profile"`
	Strategy         string                     `json:"strategy"`
	Enabled          bool                       `json:"enabled"`
	Timeout          string                     `json:"timeout,omitempty"`
	Upstreams        []config.DNSUpstreamConfig `json:"upstreams"`
	InterceptsPort53 bool                       `json:"intercepts_port_53"`
}

func (s *Server) handleDNS(w http.ResponseWriter, r *http.Request) {
	cfg := s.engine.Config()
	profile, err := selectAPIProfile(cfg, r.URL.Query().Get("profile"))
	if err != nil {
		writeProfileSelectionError(w, err)
		return
	}
	writeJSON(w, dnsSnapshot(profile))
}

func dnsSnapshot(profile *config.Profile) dnsPayload {
	payload := dnsPayload{
		Profile:   profile.Name,
		Strategy:  "route",
		Upstreams: []config.DNSUpstreamConfig{},
	}
	if !profile.DNS.Enabled {
		return payload
	}
	timeout := profile.DNS.Timeout.Std()
	if timeout == 0 {
		timeout = defaultDNSProxyTimeout
	}
	payload.Strategy = "encrypted"
	payload.Enabled = true
	payload.Timeout = timeout.String()
	payload.Upstreams = append([]config.DNSUpstreamConfig(nil), profile.DNS.Upstreams...)
	payload.InterceptsPort53 = true
	return payload
}
