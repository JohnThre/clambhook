package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/license"
)

// licenseSnapshotJSON encodes a license.Snapshot for use as an on-disk fixture.
func licenseSnapshotJSON(t *testing.T, snap license.Snapshot) string {
	t.Helper()
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	return string(b)
}

// writeLicenseFixture writes the snapshot JSON to a temp file and returns its
// path.
func writeLicenseFixture(t *testing.T, snap license.Snapshot) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "license.json")
	if err := os.WriteFile(p, []byte(licenseSnapshotJSON(t, snap)), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return p
}

// newLicenseServer returns a server wired with the given license path, using
// the same bound-address pattern as newBoundServer so guardMiddleware allows
// loopback native requests.
func newLicenseServer(t *testing.T, licensePath string) *Server {
	t.Helper()
	srv := NewWithOptions(engine.New(testAuthConfig(), nil), events.NewBus(events.DefaultConfig()), Options{
		LicensePath: licensePath,
	})
	srv.addr = "127.0.0.1:9090"
	return srv
}

// licenseRequest dispatches a request through the full middleware chain the
// same way guardRequest does, so the license gate runs in its real position.
func licenseRequest(t *testing.T, srv *Server, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Host = "127.0.0.1:9090"
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	return rec
}

func ptrTime(t time.Time) *time.Time { return &t }

// TestLicenseGateBlocksLockedTrial covers the core regression: an expired
// trial (ReasonLocked) must not be able to drive state-changing routes through
// the daemon API. Before the middleware, connect succeeded with no license
// check.
func TestLicenseGateBlocksLockedTrial(t *testing.T) {
	snap := license.Snapshot{
		TrialStartDate: ptrTime(license.UTCDate(2026, 1, 1)), // expired well before now
	}
	path := writeLicenseFixture(t, snap)
	srv := newLicenseServer(t, path)

	cases := []struct {
		name, method, target string
	}{
		{"connect", http.MethodPost, "/api/v1/connect"},
		{"set active profile", http.MethodPut, "/api/v1/profiles/active"},
		{"replace rules", http.MethodPut, "/api/v1/rules"},
		{"create rule", http.MethodPost, "/api/v1/rules"},
		{"update dns", http.MethodPut, "/api/v1/dns"},
		{"update config settings", http.MethodPut, "/api/v1/config/settings"},
		{"regenerate developer CA", http.MethodPost, "/api/v1/developer/ca/regenerate"},
		{"resolve prompt", http.MethodPost, "/api/v1/prompts/abc/resolve"},
		{"delete developer entries", http.MethodDelete, "/api/v1/developer/entries"},
		{"delete developer map rule", http.MethodDelete, "/api/v1/developer/map-rules/xyz"},
		{"delete developer breakpoint rule", http.MethodDelete, "/api/v1/developer/breakpoint-rules/xyz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := licenseRequest(t, srv, tc.method, tc.target)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s: status = %d body=%q, want 403", tc.method, tc.target, rec.Code, rec.Body.String())
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err == nil {
				if body["error"] == "" {
					t.Fatalf("expected error message in body, got %q", rec.Body.String())
				}
			}
		})
	}
}

// TestLicenseGateAllowsReadRoutesWhenLocked ensures a locked license does not
// blind the user: read-only GET routes must still return data so the UI can
// show status and diagnostics.
func TestLicenseGateAllowsReadRoutesWhenLocked(t *testing.T) {
	snap := license.Snapshot{
		TrialStartDate: ptrTime(license.UTCDate(2026, 1, 1)),
	}
	path := writeLicenseFixture(t, snap)
	srv := newLicenseServer(t, path)

	readCases := []struct {
		name, target string
	}{
		{"status", "/api/v1/status"},
		{"profiles", "/api/v1/profiles"},
		{"servers", "/api/v1/servers"},
		{"rules", "/api/v1/rules"},
		{"config settings", "/api/v1/config/settings"},
		{"developer status", "/api/v1/developer/status"},
	}
	for _, tc := range readCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := licenseRequest(t, srv, http.MethodGet, tc.target)
			if rec.Code == http.StatusForbidden {
				t.Fatalf("read route %s blocked by license gate: %d body=%q", tc.target, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestLicenseGateAllowsDisconnectWhenLocked verifies the one intentional
// exemption: a locked user must always be able to stop routing.
func TestLicenseGateAllowsDisconnectWhenLocked(t *testing.T) {
	snap := license.Snapshot{
		TrialStartDate: ptrTime(license.UTCDate(2026, 1, 1)),
	}
	path := writeLicenseFixture(t, snap)
	srv := newLicenseServer(t, path)

	rec := licenseRequest(t, srv, http.MethodPost, "/api/v1/disconnect")
	if rec.Code == http.StatusForbidden {
		t.Fatalf("disconnect blocked by license gate when locked: %d body=%q", rec.Code, rec.Body.String())
	}
}

// TestLicenseGateAllowsTemporaryRuleCleanupWhenLocked verifies the second
// intentional exemption: a locked user can still delete temporary rules to
// tear down state from before lockout.
func TestLicenseGateAllowsTemporaryRuleCleanupWhenLocked(t *testing.T) {
	snap := license.Snapshot{
		TrialStartDate: ptrTime(license.UTCDate(2026, 1, 1)),
	}
	path := writeLicenseFixture(t, snap)
	srv := newLicenseServer(t, path)

	rec := licenseRequest(t, srv, http.MethodDelete, "/api/v1/rules/temporary/abc")
	if rec.Code == http.StatusForbidden {
		t.Fatalf("temporary rule cleanup blocked by license gate when locked: %d body=%q", rec.Code, rec.Body.String())
	}
}

// TestLicenseGateAllowsActiveTrial confirms a within-trial license can still
// drive state-changing routes.
func TestLicenseGateAllowsActiveTrial(t *testing.T) {
	snap := license.Snapshot{
		TrialStartDate: ptrTime(time.Now().AddDate(0, 0, -1)), // started yesterday
	}
	path := writeLicenseFixture(t, snap)
	srv := newLicenseServer(t, path)

	rec := licenseRequest(t, srv, http.MethodPost, "/api/v1/connect")
	if rec.Code == http.StatusForbidden {
		t.Fatalf("active trial connect blocked: %d body=%q", rec.Code, rec.Body.String())
	}
}

// TestLicenseGateAllowsLifetimeLicense confirms a paid lifetime license with a
// valid update window passes the gate.
func TestLicenseGateAllowsLifetimeLicense(t *testing.T) {
	snap := license.Snapshot{
		Transactions: []license.Transaction{
			{ProductID: license.LifetimeUnlockProductID, PurchaseDate: license.UTCDate(2026, 6, 3)},
		},
		LastVerifiedAt: ptrTime(license.UTCDate(2026, 6, 10)),
	}
	path := writeLicenseFixture(t, snap)
	srv := newLicenseServer(t, path)

	rec := licenseRequest(t, srv, http.MethodPost, "/api/v1/connect")
	if rec.Code == http.StatusForbidden {
		t.Fatalf("lifetime license connect blocked: %d body=%q", rec.Code, rec.Body.String())
	}
}

// TestLicenseGateFailsClosedOnMissingFile verifies the fail-closed contract:
// when LicensePath is set but the file is absent or unreadable, the gate
// returns 403 rather than allowing the request through.
func TestLicenseGateFailsClosedOnMissingFile(t *testing.T) {
	srv := newLicenseServer(t, filepath.Join(t.TempDir(), "does-not-exist.json"))

	rec := licenseRequest(t, srv, http.MethodPost, "/api/v1/connect")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing license file: status = %d, want 403", rec.Code)
	}
}

// TestLicenseGateFailsClosedOnMalformedFile verifies a corrupt license file
// fails closed (403), not panics.
func TestLicenseGateFailsClosedOnMalformedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "license.json")
	if err := os.WriteFile(p, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	srv := newLicenseServer(t, p)

	rec := licenseRequest(t, srv, http.MethodPost, "/api/v1/connect")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("malformed license file: status = %d, want 403", rec.Code)
	}
}

// TestLicenseGateDisabledWhenPathEmpty confirms the no-op behavior: when
// LicensePath is empty the middleware passes every request through, preserving
// the prior behavior for local development and tests.
func TestLicenseGateDisabledWhenPathEmpty(t *testing.T) {
	srv := newLicenseServer(t, "")

	rec := licenseRequest(t, srv, http.MethodPost, "/api/v1/connect")
	if rec.Code == http.StatusForbidden {
		t.Fatalf("empty license path should disable gate, got 403: %q", rec.Body.String())
	}
}

// TestLicenseGateCachesDecision verifies the decision is cached: two requests
// reuse one file read (verified by swapping the file between requests and
// observing the cache still serves the first decision within the TTL).
func TestLicenseGateCachesDecision(t *testing.T) {
	snap := license.Snapshot{
		TrialStartDate: ptrTime(time.Now().AddDate(0, 0, -1)),
	}
	path := writeLicenseFixture(t, snap)
	srv := newLicenseServer(t, path)

	// First request loads + caches the decision.
	rec := licenseRequest(t, srv, http.MethodPost, "/api/v1/connect")
	if rec.Code == http.StatusForbidden {
		t.Fatalf("first request should pass: %d", rec.Code)
	}

	// Replace the file with a locked snapshot. Within the cache TTL the
	// in-memory decision is still the active trial, so the second request
	// must pass too.
	lockedSnap := license.Snapshot{
		TrialStartDate: ptrTime(license.UTCDate(2026, 1, 1)),
	}
	if err := os.WriteFile(path, []byte(licenseSnapshotJSON(t, lockedSnap)), 0o600); err != nil {
		t.Fatalf("rewrite fixture: %v", err)
	}
	rec = licenseRequest(t, srv, http.MethodPost, "/api/v1/connect")
	if rec.Code == http.StatusForbidden {
		t.Fatalf("cached decision should still allow within TTL: %d", rec.Code)
	}

	// After expiring the cache, the locked snapshot takes effect.
	srv.licenseMu.Lock()
	srv.licenseCache = licenseCacheEntry{}
	srv.licenseMu.Unlock()
	rec = licenseRequest(t, srv, http.MethodPost, "/api/v1/connect")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("after cache expiry locked snapshot should block: %d", rec.Code)
	}
}