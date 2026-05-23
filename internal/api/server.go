package api

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/traffic"
)

// Server is the HTTP API server for frontend communication.
type Server struct {
	engine    *engine.Engine
	bus       *events.Bus
	traffic   *traffic.Store
	authToken string
	server    *http.Server
}

// New creates a new API server. bus may be nil to disable the
// /api/v1/events WebSocket endpoint.
func New(eng *engine.Engine, bus *events.Bus) *Server {
	return NewWithOptions(eng, bus, Options{})
}

// NewWithOptions creates a new API server with optional route protection.
func NewWithOptions(eng *engine.Engine, bus *events.Bus, opts Options) *Server {
	s := &Server{
		engine:    eng,
		bus:       bus,
		traffic:   opts.TrafficStore,
		authToken: strings.TrimSpace(opts.AuthToken),
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.server = &http.Server{Handler: s.authMiddleware(mux)}
	return s
}

// Start begins listening on the given address.
func (s *Server) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("api server listening on %s", addr)
	go s.server.Serve(ln)
	return nil
}

// Shutdown gracefully shuts down the API server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}
