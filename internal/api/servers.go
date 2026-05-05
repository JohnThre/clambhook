package api

import (
	"net/http"

	"github.com/clambhook/clambhook/internal/geo"
)

type serversPayload struct {
	Profile string         `json:"profile"`
	Chains  []chainPayload `json:"chains"`
}

type chainPayload struct {
	Name    string          `json:"name"`
	Servers []serverPayload `json:"servers"`
}

type serverPayload struct {
	Name     string        `json:"name"`
	Address  string        `json:"address"`
	Protocol string        `json:"protocol"`
	Geo      *geo.Location `json:"geo"`
	GeoError string        `json:"geo_error,omitempty"`
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
			Name:    ch.Name,
			Servers: make([]serverPayload, 0, len(ch.Servers)),
		}
		for _, server := range ch.Servers {
			loc, lookupErr := geoReader.Lookup(server.Address)
			row := serverPayload{
				Name:     server.Name,
				Address:  server.Address,
				Protocol: server.Protocol,
				Geo:      loc,
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
