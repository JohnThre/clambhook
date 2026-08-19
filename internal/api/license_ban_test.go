package api

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/JohnThre/clambhook/internal/license"
)

const (
	banTestPrivB64 = "MC4CAQAwBQYDK2VwBCIEIOr839ef5i0O7VYR8Ax83UctlcSpe+BkETpCzUq2od+x"
	banTestPubB64  = "6EIR6S1pCEC2HLZijYwnbP7rrXh3LtqONaDqE6iPhPM="
	banTestKeyID   = "clambhook-grant-test-v1"
)

func banTestSign(t *testing.T, msg string) string {
	t.Helper()
	pkcs8, _ := base64.StdEncoding.DecodeString(banTestPrivB64)
	keyAny, err := x509.ParsePKCS8PrivateKey(pkcs8)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(keyAny.(ed25519.PrivateKey), []byte(msg)))
}

func writeLicenseStateFixture(t *testing.T, state license.LicenseState) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "license.json")
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func signedBanMarker(t *testing.T, installID string) license.BanMarker {
	t.Helper()
	m := license.BanMarker{
		BanID:        "clhb_1",
		Reason:       "cracked",
		Source:       "verification",
		BannedAt:     license.UTCDate(2026, 7, 1),
		InstallID:    installID,
		KeyID:        banTestKeyID,
		SupportEmail: license.SupportEmail,
		DisputeURL:   license.DisputeURL,
	}
	m.Signature = banTestSign(t, license.CanonicalizeBan(m))
	return m
}

func TestGETLicenseBanReturnsActiveMarker(t *testing.T) {
	restore := license.SetVerifyingKeyForTest(banTestKeyID, banTestPubB64)
	defer restore()

	state := license.LicenseState{
		InstallID: "install-1",
		BanMarker: ptrBanMarker(signedBanMarker(t, "install-1")),
		Snapshot:  license.Snapshot{Transactions: []license.Transaction{{ProductID: license.LifetimeUnlockProductID, PurchaseDate: license.UTCDate(2026, 6, 3)}}},
	}
	path := writeLicenseStateFixture(t, state)
	srv := newLicenseServer(t, path)

	rec := licenseRequest(t, srv, http.MethodGet, "/api/v1/license/ban")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["banned"] != true {
		t.Fatalf("banned = %v, want true; body=%s", body["banned"], rec.Body.String())
	}
	if body["ban_reason"] != "cracked" {
		t.Fatalf("ban_reason = %v, want cracked", body["ban_reason"])
	}
	if body["support_email"] != license.SupportEmail {
		t.Fatalf("support_email = %v, want %s", body["support_email"], license.SupportEmail)
	}
	if body["dispute_url"] != license.DisputeURL {
		t.Fatalf("dispute_url = %v, want %s", body["dispute_url"], license.DisputeURL)
	}
}

func TestGETLicenseBanNoneWhenNoBan(t *testing.T) {
	restore := license.SetVerifyingKeyForTest(banTestKeyID, banTestPubB64)
	defer restore()

	g := license.ServerGrant{
		Version: 1, IssuedAt: license.UTCDate(2026, 7, 1), ExpiresAt: license.UTCDate(2027, 7, 1),
		Reason: license.ReasonLifetime, HasLifetimeUnlock: true,
		UpdateCutoffDate: ptrTime(license.UTCDate(2027, 7, 1)),
		Transactions:     []license.Transaction{{ProductID: license.LifetimeUnlockProductID, PurchaseDate: license.UTCDate(2026, 6, 3)}},
		KeyID:            banTestKeyID,
	}
	g.Signature = banTestSign(t, license.CanonicalizeGrant(g))

	state := license.LicenseState{
		InstallID: "install-1",
		Grant:     &g,
		Snapshot:  license.Snapshot{Transactions: g.Transactions},
	}
	path := writeLicenseStateFixture(t, state)
	srv := newLicenseServer(t, path)

	rec := licenseRequest(t, srv, http.MethodGet, "/api/v1/license/ban")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["banned"] == true {
		t.Fatalf("expected no ban, body=%s", rec.Body.String())
	}
}

func TestLicenseGateBannedFailsClosedButAllowsDisconnect(t *testing.T) {
	restore := license.SetVerifyingKeyForTest(banTestKeyID, banTestPubB64)
	defer restore()

	state := license.LicenseState{
		InstallID: "install-1",
		BanMarker: ptrBanMarker(signedBanMarker(t, "install-1")),
		Snapshot:  license.Snapshot{Transactions: []license.Transaction{{ProductID: license.LifetimeUnlockProductID, PurchaseDate: license.UTCDate(2026, 6, 3)}}},
	}
	path := writeLicenseStateFixture(t, state)
	srv := newLicenseServer(t, path)

	// A state-changing route (connect) is blocked while banned.
	connect := licenseRequest(t, srv, http.MethodPost, "/api/v1/connect")
	if connect.Code != http.StatusForbidden {
		t.Fatalf("connect = %d, want 403 (banned must fail closed)", connect.Code)
	}

	// Read-only GET status is allowed so the user can still observe state.
	status := licenseRequest(t, srv, http.MethodGet, "/api/v1/status")
	if status.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (reads allowed while banned)", status.Code)
	}

	// disconnect is exempt so a banned user can always stop routing.
	disconnect := licenseRequest(t, srv, http.MethodPost, "/api/v1/disconnect")
	if disconnect.Code == http.StatusForbidden {
		t.Fatalf("disconnect = %d, must not be license-gated while banned", disconnect.Code)
	}
}

func ptrBanMarker(m license.BanMarker) *license.BanMarker { return &m }
