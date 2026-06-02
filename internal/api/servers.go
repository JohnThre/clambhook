package api

import (
	"net/http"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/geo"
	"github.com/JohnThre/clambhook/internal/protocol"
)

type serversPayload struct {
	Profile string         `json:"profile"`
	Chains  []chainPayload `json:"chains"`
}

type chainPayload struct {
	Name         string                `json:"name"`
	HopCount     int                   `json:"hop_count"`
	Capabilities protocol.Capabilities `json:"capabilities"`
	Servers      []serverPayload       `json:"servers"`
}

type serverPayload struct {
	Name         string                `json:"name"`
	Address      string                `json:"address"`
	Protocol     string                `json:"protocol"`
	Capabilities protocol.Capabilities `json:"capabilities"`
	Geo          *geo.Location         `json:"geo"`
	GeoError     string                `json:"geo_error,omitempty"`
}

func (s *Server) handleServers(w http.ResponseWriter, r *http.Request) {
	cfg := s.engine.Config()
	profile, err := cfg.ActiveProfile()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	geoReader := s.engine.GeoReader()
	payload := serversPayload{
		Profile: profile.Name,
		Chains:  make([]chainPayload, 0, len(profile.Chains)),
	}

	for _, ch := range profile.Chains {
		cp := chainPayload{
			Name:         ch.Name,
			HopCount:     len(ch.Servers),
			Capabilities: chainCapabilities(ch),
			Servers:      make([]serverPayload, 0, len(ch.Servers)),
		}
		for _, server := range ch.Servers {
			loc, lookupErr := geoReader.Lookup(server.Address)
			row := serverPayload{
				Name:         server.Name,
				Address:      server.Address,
				Protocol:     server.Protocol,
				Capabilities: protocol.CapabilitiesForProtocol(server.Protocol),
				Geo:          loc,
			}
			if lookupErr != nil {
				row.Geo = &geo.Location{}
				row.GeoError = lookupErr.Error()
			}
			cp.Servers = append(cp.Servers, row)
		}
		payload.Chains = append(payload.Chains, cp)
	}

	writeJSON(w, payload)
}

func chainCapabilities(ch config.ChainConfig) protocol.Capabilities {
	caps := protocol.Capabilities{
		TCP:     len(ch.Servers) > 0,
		UDPMode: protocol.UDPModeUnsupported,
	}
	if len(ch.Servers) == 0 {
		caps.UDPReason = "chain has no servers"
		return caps
	}
	last := ch.Servers[len(ch.Servers)-1]
	lastCaps := protocol.CapabilitiesForProtocol(last.Protocol)
	if !lastCaps.UDP {
		caps.UDPReason = lastCaps.UDPReason
		if caps.UDPReason == "" {
			caps.UDPReason = "protocol does not support UDP"
		}
		return caps
	}
	if len(ch.Servers) == 1 {
		return lastCaps
	}
	if lastCaps.UDPMode != protocol.UDPModeStream {
		caps.UDPReason = lastCaps.UDPReason
		if caps.UDPReason == "" {
			caps.UDPReason = "final protocol cannot carry UDP through an upstream chain"
		}
		return caps
	}
	caps.UDP = true
	caps.UDPMode = protocol.UDPModeStream
	return caps
}
