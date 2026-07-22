package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/JohnThre/clambhook/internal/license"
)

// licenseCacheTTL bounds how long a decoded license decision is reused before
// the snapshot file is re-read and re-evaluated. The evaluation itself is pure
// and cheap; the TTL exists so a locked-out user who activates a license from
// another client sees access restored within this window without a daemon
// restart.
const licenseCacheTTL = 10 * time.Second

// licenseGatedMethods are the HTTP methods that mutate daemon state and are
// therefore gated by the license middleware. GET (read-only) is intentionally
// excluded so a locked user can still observe state and diagnostics.
//
// disconnect is excluded everywhere (see isLicenseGatedRequest) so a locked
// user can always stop routing.
func (s *Server) licenseGatedMethods() map[string]struct{} {
	return map[string]struct{}{
		http.MethodPost:   {},
		http.MethodPut:    {},
		http.MethodDelete: {},
	}
}

// licenseMiddleware gates state-changing routes on the cached license
// decision. It runs inside the authMiddleware (after bearer-token auth) so a
// hostile origin rejected by guardMiddleware never reaches this check. When
// LicensePath is empty the middleware is a no-op, preserving the current
// behavior for local development and tests.
func (s *Server) licenseMiddleware(next http.Handler) http.Handler {
	if s.licensePath == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.isLicenseGatedRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		decision, err := s.licenseDecision()
		if err != nil {
			log.Printf("api: license gate: %v", err)
			writeLicenseForbidden(w, "license unavailable")
			return
		}
		if !decision.CanUseApp() {
			writeLicenseForbidden(w, "license required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLicenseGatedRequest reports whether this specific request should be gated.
// A request is gated when its method is mutating, with two exemptions so a
// locked user can always stop routing and tear down temporary rules:
//   - POST /api/v1/disconnect is never gated.
//   - DELETE /api/v1/rules/temporary/{id} is never gated (cleanup).
func (s *Server) isLicenseGatedRequest(r *http.Request) bool {
	if r.URL.Path == "/api/v1/disconnect" {
		return false
	}
	if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/rules/temporary/") {
		return false
	}
	_, gated := s.licenseGatedMethods()[r.Method]
	return gated
}

func writeLicenseForbidden(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// licenseDecision returns a cached license.Decision, re-reading and
// re-evaluating the snapshot file at most once per licenseCacheTTL. On any read
// or decode error it returns the error so the caller fails closed.
func (s *Server) licenseDecision() (license.Decision, error) {
	s.licenseMu.Lock()
	defer s.licenseMu.Unlock()
	now := time.Now()
	if s.licenseCache.exp.After(now) {
		return s.licenseCache.decision, nil
	}
	dec, err := s.readLicenseDecision(now)
	if err != nil {
		return license.Decision{}, err
	}
	s.licenseCache = licenseCacheEntry{decision: dec, exp: now.Add(licenseCacheTTL)}
	return dec, nil
}

// readLicenseDecision reads, decodes, and evaluates the license snapshot. It
// is called under licenseMu.
func (s *Server) readLicenseDecision(now time.Time) (license.Decision, error) {
	data, err := os.ReadFile(s.licensePath)
	if err != nil {
		return license.Decision{}, err
	}
	var snap license.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return license.Decision{}, err
	}
	return license.Evaluate(snap, nil, now), nil
}

type licenseCacheEntry struct {
	decision license.Decision
	exp      time.Time
}

// SetLicensePathForTest swaps the license path and clears the cache. It is
// intended only for tests that need to point the middleware at a fixture.
func (s *Server) SetLicensePathForTest(path string) {
	s.licenseMu.Lock()
	defer s.licenseMu.Unlock()
	s.licensePath = path
	s.licenseCache = licenseCacheEntry{}
}