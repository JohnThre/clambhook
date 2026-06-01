package api

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/traffic"
)

// Server is the HTTP API server for frontend communication.
type Server struct {
	engine     *engine.Engine
	bus        *events.Bus
	traffic    *traffic.Store
	authToken  string
	configPath string
	server     *http.Server
	mu         sync.RWMutex
	addr       string
}

// New creates a new API server. bus may be nil to disable the
// /api/v1/events WebSocket endpoint.
func New(eng *engine.Engine, bus *events.Bus) *Server {
	return NewWithOptions(eng, bus, Options{})
}

// NewWithOptions creates a new API server with optional route protection.
func NewWithOptions(eng *engine.Engine, bus *events.Bus, opts Options) *Server {
	s := &Server{
		engine:     eng,
		bus:        bus,
		traffic:    opts.TrafficStore,
		authToken:  strings.TrimSpace(opts.AuthToken),
		configPath: strings.TrimSpace(opts.ConfigPath),
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.server = &http.Server{
		Handler:           s.authMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	return s
}

// Start begins listening on the given address.
func (s *Server) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.addr = ln.Addr().String()
	s.mu.Unlock()
	log.Printf("api server listening on %s", ln.Addr())
	go func() {
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("api server stopped unexpectedly: %v", err)
		}
	}()
	return nil
}

// Addr returns the bound API address, or an empty string before Start.
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

// SetTrafficStore swaps the store backing /api/v1/traffic. Passing nil keeps
// the endpoint enabled but returns the same empty snapshot shape as before.
func (s *Server) SetTrafficStore(store *traffic.Store) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traffic = store
}

func (s *Server) trafficStore() *traffic.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.traffic
}

// Shutdown gracefully shuts down the API server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}
