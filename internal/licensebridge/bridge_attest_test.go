package licensebridge

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/JohnThre/clambhook/internal/license"
)

const (
	bridgeTestPrivB64 = "MC4CAQAwBQYDK2VwBCIEIOr839ef5i0O7VYR8Ax83UctlcSpe+BkETpCzUq2od+x"
	bridgeTestPubB64  = "6EIR6S1pCEC2HLZijYwnbP7rrXh3LtqONaDqE6iPhPM="
	bridgeTestKeyID   = "clambhook-grant-test-v1"
)

func bridgeTestSign(t *testing.T, msg string) string {
	t.Helper()
	pkcs8, _ := base64.StdEncoding.DecodeString(bridgeTestPrivB64)
	keyAny, err := x509.ParsePKCS8PrivateKey(pkcs8)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(keyAny.(ed25519.PrivateKey), []byte(msg)))
}

func TestAttestGenuineVerifiesSignedGrant(t *testing.T) {
	restore := license.SetVerifyingKeyForTest(bridgeTestKeyID, bridgeTestPubB64)
	defer restore()
	BuildSecret = "test-secret"
	defer func() { BuildSecret = "" }()

	purchase := license.UTCDate(2026, 6, 3)
	grant := license.ServerGrant{
		Version: 1, IssuedAt: purchase, ExpiresAt: license.UTCDate(2027, 6, 3),
		Reason: license.ReasonLifetime, HasLifetimeUnlock: true,
		UpdateCutoffDate: ptrTime(purchase.AddDate(1, 0, 0)),
		Transactions:     []license.Transaction{{ProductID: license.LifetimeUnlockProductID, PurchaseDate: purchase}},
		KeyID:            bridgeTestKeyID,
	}
	grant.Signature = bridgeTestSign(t, license.CanonicalizeGrant(grant))

	resp := license.ServerResponse{
		Grant: grant,
		Snapshot: license.GrantSnapshot{
			Reason: license.ReasonLifetime, HasLifetimeUnlock: true,
			Transactions: grant.Transactions,
		},
		DeviceState: license.DeviceState{
			CurrentDeviceID: "device-1", MaxActiveDevices: license.MaxActiveDevices,
			Devices:         []license.Device{{DeviceID: "device-1", InstallID: "install-1", ActivatedAt: purchase}},
			PaymentProvider: &license.ProviderCreem,
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/devices/attest" {
			t.Errorf("posted to %s, want /v1/devices/attest", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	reg := license.DeviceRegistration{InstallID: "install-1", Platform: "macos", Architecture: "arm64", AppVersion: "1.0.0"}
	regJSON, _ := json.Marshal(reg)
	out, err := AttestLicenseJSON(srv.URL, string(regJSON), "good-hash", nil, purchase.AddDate(0, 1, 0).UnixMilli())
	if err != nil {
		t.Fatalf("attest genuine: %v", err)
	}
	var res attestResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Genuine || res.Banned {
		t.Fatalf("expected genuine, got %+v", res)
	}
	if res.Grant == nil || res.Grant.KeyID != bridgeTestKeyID {
		t.Fatalf("grant not carried: %+v", res.Grant)
	}
	if res.Decision.Reason != license.ReasonLifetime {
		t.Fatalf("decision = %s, want lifetime", res.Decision.Reason)
	}
}

func TestAttestBannedVerifiesAndReturnsBanMarker(t *testing.T) {
	restore := license.SetVerifyingKeyForTest(bridgeTestKeyID, bridgeTestPubB64)
	defer restore()
	BuildSecret = "test-secret"
	defer func() { BuildSecret = "" }()

	now := license.UTCDate(2026, 7, 1)
	m := license.BanMarker{
		BanID: "clhb_1", Reason: "cracked", Source: "verification",
		BannedAt: now, InstallID: "install-1", KeyID: bridgeTestKeyID,
	}
	m.Signature = bridgeTestSign(t, license.CanonicalizeBan(m))
	thread := "https://swiphtgroup.com/forum/t/123"
	m.DisputeThreadURL = &thread
	m.SupportEmail = license.SupportEmail
	m.DisputeURL = license.DisputeURL

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		banJSON, _ := json.Marshal(m)
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"banned":             true,
			"ban_reason":         m.Reason,
			"support_email":      m.SupportEmail,
			"dispute_url":        m.DisputeURL,
			"dispute_thread_url": m.DisputeThreadURL,
			"ban":                json.RawMessage(banJSON),
		})
	}))
	defer srv.Close()

	reg := license.DeviceRegistration{InstallID: "install-1", Platform: "macos"}
	regJSON, _ := json.Marshal(reg)
	out, err := AttestLicenseJSON(srv.URL, string(regJSON), "patched-hash", nil, now.UnixMilli())
	if err != nil {
		t.Fatalf("attest banned should not error: %v", err)
	}
	var res attestResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Banned || res.Genuine {
		t.Fatalf("expected banned, got %+v", res)
	}
	if res.BanMarker == nil || res.BanMarker.BanID != "clhb_1" {
		t.Fatalf("ban marker not carried: %+v", res.BanMarker)
	}
	if res.Decision.Reason != license.ReasonBanned || res.Decision.CanUseApp() {
		t.Fatalf("decision = %+v, want banned/locked", res.Decision)
	}
}

func TestAttestForgedBannedVerdictRejected(t *testing.T) {
	restore := license.SetVerifyingKeyForTest(bridgeTestKeyID, bridgeTestPubB64)
	defer restore()
	BuildSecret = "test-secret"
	defer func() { BuildSecret = "" }()

	now := license.UTCDate(2026, 7, 1)
	m := license.BanMarker{
		BanID: "clhb_1", Reason: "cracked", Source: "verification",
		BannedAt: now, InstallID: "install-1", KeyID: bridgeTestKeyID,
		Signature: base64.StdEncoding.EncodeToString([]byte("forged-signature")),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		banJSON, _ := json.Marshal(m)
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"banned": true, "ban_reason": "cracked",
			"support_email": "support@swiphtgroup.com", "dispute_url": "https://x",
			"ban": json.RawMessage(banJSON),
		})
	}))
	defer srv.Close()

	reg := license.DeviceRegistration{InstallID: "install-1"}
	regJSON, _ := json.Marshal(reg)
	_, err := AttestLicenseJSON(srv.URL, string(regJSON), "patched", nil, now.UnixMilli())
	if err == nil {
		t.Fatal("a forged ban verdict signature should be rejected")
	}
}

func TestAttestNetworkErrorReturnsError(t *testing.T) {
	BuildSecret = "test-secret"
	defer func() { BuildSecret = "" }()
	reg := license.DeviceRegistration{InstallID: "install-1"}
	regJSON, _ := json.Marshal(reg)
	// Point at a closed server: httptest server closed before the call.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	_, err := AttestLicenseJSON(srv.URL, string(regJSON), "h", nil, 0)
	if err == nil {
		t.Fatal("network error should surface as an error")
	}
}

func TestBuildAttestationJSONIncludesBuildToken(t *testing.T) {
	BuildSecret = "test-secret"
	defer func() { BuildSecret = "" }()
	out, err := BuildAttestationJSON("install-1", "hash", nil, "n", license.UTCDate(2026, 6, 10).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	var att map[string]any
	if err := json.Unmarshal([]byte(out), &att); err != nil {
		t.Fatal(err)
	}
	if att["key_id"] != license.CLAMBHOOKGrantKeyID {
		t.Fatalf("key_id = %v, want %s", att["key_id"], license.CLAMBHOOKGrantKeyID)
	}
	if att["build_token"] == "" || att["build_token"] == nil {
		t.Fatal("build_token must be present")
	}
	if att["binary_hash"] != "hash" {
		t.Fatalf("binary_hash = %v, want hash", att["binary_hash"])
	}
}
