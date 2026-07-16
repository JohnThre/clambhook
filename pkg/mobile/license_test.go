package mobile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/license"
)

func TestEnsureAndEvaluateLicenseTrial(t *testing.T) {
	nowMillis := license.UTCDate(2026, 7, 15).UnixMilli()
	seeded, err := EnsureLicenseTrialJSON("", nowMillis)
	if err != nil {
		t.Fatal(err)
	}
	var snap license.Snapshot
	if err := json.Unmarshal([]byte(seeded), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.TrialStartDate == nil {
		t.Fatal("trial start not seeded")
	}

	decJSON, err := EvaluateLicenseJSON(seeded, nowMillis)
	if err != nil {
		t.Fatal(err)
	}
	var dec license.Decision
	if err := json.Unmarshal([]byte(decJSON), &dec); err != nil {
		t.Fatal(err)
	}
	if dec.Reason != license.ReasonTrial {
		t.Fatalf("reason = %s, want trial", dec.Reason)
	}

	// After the trial month, the app locks.
	lockedJSON, err := EvaluateLicenseJSON(seeded, license.UTCDate(2026, 8, 16).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	var locked license.Decision
	_ = json.Unmarshal([]byte(lockedJSON), &locked)
	if locked.CanUseApp() {
		t.Fatal("expected locked after trial")
	}
}

func TestLicenseStatusSurfacesExpiredTrialBanner(t *testing.T) {
	snap := license.Snapshot{TrialStartDate: ptrTime(license.UTCDate(2026, 6, 3))}
	snapJSON, _ := json.Marshal(snap)

	statusJSON, err := LicenseStatusJSON(string(snapJSON), 0, license.UTCDate(2026, 8, 4).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	var payload licenseStatusPayload
	if err := json.Unmarshal([]byte(statusJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ExpiredTrial == nil || payload.ExpiredTrial.PrimaryAction != license.ActionBuyLicense {
		t.Fatalf("expected buy-license banner, got %+v", payload.ExpiredTrial)
	}
	if len(payload.ProductStates) == 0 {
		t.Fatal("expected product states")
	}
}

func TestLicenseUpdateAllowedGating(t *testing.T) {
	snap := license.Snapshot{Transactions: []license.Transaction{{
		ProductID:    license.LifetimeUnlockProductID,
		PurchaseDate: license.UTCDate(2026, 6, 3),
	}}}
	snapJSON, _ := json.Marshal(snap)
	now := license.UTCDate(2028, 1, 1).UnixMilli()

	ok, err := LicenseUpdateAllowed(string(snapJSON), license.UTCDate(2027, 6, 3).UnixMilli(), now)
	if err != nil || !ok {
		t.Fatalf("release on cutoff should install: ok=%v err=%v", ok, err)
	}
	blocked, err := LicenseUpdateAllowed(string(snapJSON), license.UTCDate(2027, 6, 4).UnixMilli(), now)
	if err != nil || blocked {
		t.Fatalf("release after cutoff should not install: blocked=%v err=%v", blocked, err)
	}
}

func TestActivateLicenseAppliesServerResponse(t *testing.T) {
	purchase := license.UTCDate(2026, 6, 3)
	resp := license.ServerResponse{
		Grant: license.ServerGrant{
			Version:           1,
			IssuedAt:          purchase,
			ExpiresAt:         license.UTCDate(2027, 6, 3),
			Reason:            license.ReasonLifetime,
			HasLifetimeUnlock: true,
			Transactions:      []license.Transaction{{ProductID: license.LifetimeUnlockProductID, PurchaseDate: purchase}},
			Signature:         "sig",
		},
		Snapshot: license.GrantSnapshot{
			Reason:            license.ReasonLifetime,
			HasLifetimeUnlock: true,
			Transactions:      []license.Transaction{{ProductID: license.LifetimeUnlockProductID, PurchaseDate: purchase}},
		},
		DeviceState: license.DeviceState{
			CurrentDeviceID:  "device-1",
			MaxActiveDevices: license.MaxActiveDevices,
			Devices: []license.Device{{
				DeviceID: "device-1", InstallID: "install-1", ActivatedAt: purchase,
			}},
			PaymentProvider: &license.ProviderCreem,
		},
	}

	var gotPath string
	var gotBody license.ActivationRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := license.DeviceRegistration{
		InstallID: "install-1", DisplayName: "Pixel", Platform: "android", Architecture: "arm64", AppVersion: "0.1.0 (1)",
	}
	regJSON, _ := json.Marshal(reg)

	appliedJSON, err := ActivateLicenseJSON(server.URL+"/clambhook/license", "KEY-123", "user@example.com", string(regJSON), license.UTCDate(2026, 6, 10).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(gotPath, "/v1/devices/activate") {
		t.Fatalf("posted to %s, want .../v1/devices/activate", gotPath)
	}
	if gotBody.LicenseKey != "KEY-123" || gotBody.Device.Platform != "android" {
		t.Fatalf("request body wrong: %+v", gotBody)
	}

	var applied appliedLicensePayload
	if err := json.Unmarshal([]byte(appliedJSON), &applied); err != nil {
		t.Fatal(err)
	}
	if applied.Decision.Reason != license.ReasonLifetime || !applied.Decision.CanUseApp() {
		t.Fatalf("decision = %+v, want lifetime", applied.Decision)
	}
	if applied.Snapshot.LastVerifiedAt == nil {
		t.Fatal("applied snapshot should record verification")
	}
	if applied.DeviceState.CurrentInstallID != "install-1" {
		t.Fatalf("device state install id = %q, want install-1", applied.DeviceState.CurrentInstallID)
	}
	if applied.DeviceState.PaymentProvider == nil || applied.DeviceState.PaymentProvider.Raw != "creem" {
		t.Fatalf("payment provider not applied: %+v", applied.DeviceState.PaymentProvider)
	}
}

func TestActivateLicenseSurfacesServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"license seat limit reached"}`))
	}))
	defer server.Close()

	reg, _ := json.Marshal(license.DeviceRegistration{InstallID: "install-1", Platform: "android"})
	_, err := ActivateLicenseJSON(server.URL, "KEY", "", string(reg), 0)
	if err == nil || !strings.Contains(err.Error(), "seat limit reached") {
		t.Fatalf("expected server error surfaced, got %v", err)
	}
}

func TestMarkVerificationFailureStartsOfflineGrace(t *testing.T) {
	purchase := license.UTCDate(2026, 6, 3)
	verified := license.UTCDate(2026, 6, 10)
	snap := license.Snapshot{
		Transactions:   []license.Transaction{{ProductID: license.LifetimeUnlockProductID, PurchaseDate: purchase}},
		LastVerifiedAt: &verified,
	}
	snapJSON, _ := json.Marshal(snap)

	failMillis := license.UTCDate(2026, 6, 20).UnixMilli()
	updatedJSON, err := MarkLicenseVerificationFailureJSON(string(snapJSON), failMillis)
	if err != nil {
		t.Fatal(err)
	}
	var payload verificationFailurePayload
	if err := json.Unmarshal([]byte(updatedJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Snapshot.LastVerificationFailedAt == nil {
		t.Fatal("failure timestamp not recorded")
	}
	// Within 7-day grace of the failure the app still works.
	if payload.Decision.Reason != license.ReasonOfflineGrace {
		t.Fatalf("reason = %s, want offlineGrace", payload.Decision.Reason)
	}
}

func TestNewLicenseInstallIDIsLowercaseUnique(t *testing.T) {
	a := NewLicenseInstallID()
	b := NewLicenseInstallID()
	if a == b {
		t.Fatal("install ids should be unique")
	}
	if a != strings.ToLower(a) {
		t.Fatalf("install id not lowercase: %s", a)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
