package api

import (
	"context"
	"log"
	"net"
	"net/http"

	"github.com/clambhook/clambhook/internal/engine"
)

// Server is the HTTP API server for frontend communication.
type Server struct {
	engine *engine.Engine
	server *http.Server
}

// New creates a new API server.
func New(eng *engine.Engine) *Server {
	s := &Server{engine: eng}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.server = &http.Server{Handler: mux}
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
