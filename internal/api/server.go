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

	"github.com/JohnThre/clambhook/internal/developer"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/traffic"
)

// Server is the HTTP API server for frontend communication.
type Server struct {
	engine     *engine.Engine
	bus        *events.Bus
	traffic    *traffic.Store
	developer   *developer.Manager
	authToken   string
	configPath  string
	licensePath string
	server     *http.Server
	mu         sync.RWMutex
	// configMu serializes every on-disk configuration
	// read-modify-validate-write-reload transaction. It is deliberately
	// separate from mu (which guards mutable server fields) and is only ever
	configMu sync.Mutex

	// licenseMu guards the licensePath, licenseCache, and the cached
	// license decision. It is separate from mu and configMu so license
	// reads never block config transactions or field mutations.
	licenseMu    sync.Mutex
	licenseCache licenseCacheEntry
	addr     string
	// httpClient is used for outbound rule-set/subscription refreshes. It is
	// normally nil so production uses the default safe-redirects client; tests
	// may inject a transport that dials local listeners under a public-host URL.
	httpClient *http.Client
}

// lockConfigTxn acquires the configuration transaction mutex and returns the
// matching unlock function so callers can guard a whole transaction with a
// single deferred statement:
//
//	defer s.lockConfigTxn()()
//
// Every code path that loads the on-disk config, mutates a section, validates,
// writes it back, and reloads the engine (rules, rule sets, policy groups and
// selections, subscriptions, developer config, active profile, config
// settings, DNS settings, and import) MUST guard its body this way. Without
// it, two concurrent edits load the same base config, mutate different
// sections, and race to write, so the last writer silently drops the other's
// change. Read-only getters intentionally stay lock-free and concurrent.
//
// The lock is non-reentrant: a guarded body must never invoke another guarded
// body, otherwise it self-deadlocks. Acquire it only after any request-body
// read so a slow client cannot stall other edits.
func (s *Server) lockConfigTxn() func() {
	s.configMu.Lock()
	return s.configMu.Unlock
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
		developer:  opts.Developer,
		authToken:   strings.TrimSpace(opts.AuthToken),
		configPath:  strings.TrimSpace(opts.ConfigPath),
		licensePath: strings.TrimSpace(opts.LicensePath),
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.server = &http.Server{
		Handler:           s.guardMiddleware(s.authMiddleware(s.licenseMiddleware(mux))),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	return s
}

// Start begins listening on the given address. It enforces that a non-empty
// bearer token is configured whenever addr is not a loopback interface, so
// non-loopback tokenless exposure is impossible for any caller.
func (s *Server) Start(addr string) error {
	if err := ValidateAuthConfig(addr, s.authToken); err != nil {
		return err
	}
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

// SetHTTPClient injects the outbound HTTP client used for rule-set and
// subscription refreshes. Tests use it to redirect requests to local
// listeners under a public-host URL.
func (s *Server) SetHTTPClient(c *http.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.httpClient = c
}

// getHTTPClient returns the outbound HTTP client for rule-set and subscription
// refreshes, acquiring the read lock for thread-safe access.
func (s *Server) getHTTPClient() *http.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.httpClient
}

// SetDeveloper swaps the manager backing /api/v1/developer/*.
func (s *Server) SetDeveloper(dev *developer.Manager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.developer = dev
}

func (s *Server) developerManager() *developer.Manager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.developer
}

// Shutdown gracefully shuts down the API server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}
