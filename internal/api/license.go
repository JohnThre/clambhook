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

// readLicenseDecision reads, decodes, and evaluates the license state. It
// accepts either the new LicenseState envelope (with the signed grant + ban
// marker) or a bare Snapshot file written by an older UI (wrapped as a legacy
// envelope). It is called under licenseMu.
func (s *Server) readLicenseDecision(now time.Time) (license.Decision, error) {
	data, err := os.ReadFile(s.licensePath)
	if err != nil {
		return license.Decision{}, err
	}
	var state license.LicenseState
	if json.Unmarshal(data, &state) == nil && (state.Grant != nil || state.InstallID != "" || state.BanMarker != nil || state.LegacyUnsignedOK) {
		return license.EvaluateState(state, nil, now), nil
	}
	// Bare Snapshot (pre-genuine-verification file written by an older UI):
	// wrap as a legacy LicenseState so an already-licensed offline user keeps
	// working until the verifier attests and persists a signed grant.
	var snap license.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return license.Decision{}, err
	}
	return license.EvaluateState(license.LicenseState{Snapshot: snap, LegacyUnsignedOK: true}, nil, now), nil
}

type licenseCacheEntry struct {
	decision license.Decision
	exp      time.Time
}

// LicenseBanState returns the active ban marker for GET /api/v1/license/ban,
// so UIs can render the dispute surface (forum + email) without a second
// round-trip. The boolean is false when no active ban is in effect.
func (s *Server) LicenseBanState() (license.BanMarker, bool) {
	s.licenseMu.Lock()
	defer s.licenseMu.Unlock()
	data, err := os.ReadFile(s.licensePath)
	if err != nil {
		return license.BanMarker{}, false
	}
	var state license.LicenseState
	if json.Unmarshal(data, &state) != nil {
		return license.BanMarker{}, false
	}
	if state.BanMarker == nil || !state.BanMarker.IsActive(time.Now()) {
		return license.BanMarker{}, false
	}
	return *state.BanMarker, true
}

// SetLicensePathForTest swaps the license path and clears the cache. It is
// intended only for tests that need to point the middleware at a fixture.
func (s *Server) SetLicensePathForTest(path string) {
	s.licenseMu.Lock()
	defer s.licenseMu.Unlock()
	s.licensePath = path
	s.licenseCache = licenseCacheEntry{}
}

// handleLicenseBan serves GET /api/v1/license/ban, returning the active ban
// marker (with the dispute surface) so UIs can render a manual-review screen.
// It is a read-only GET and is intentionally not license-gated, so a banned
// user can still read their own ban state.
func (s *Server) handleLicenseBan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.licensePath == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"banned": false})
		return
	}
	marker, ok := s.LicenseBanState()
	if !ok {
		_ = json.NewEncoder(w).Encode(map[string]any{"banned": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"banned":             true,
		"ban":                marker,
		"ban_reason":         marker.Reason,
		"support_email":      marker.SupportEmail,
		"dispute_url":        marker.DisputeURL,
		"dispute_thread_url": marker.DisputeThreadURL,
	})
}
