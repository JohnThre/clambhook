package license

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func ptr(t time.Time) *time.Time { return &t }

func lifetimeTx(purchase time.Time) Transaction {
	return Transaction{ProductID: LifetimeUnlockProductID, PurchaseDate: purchase}
}

func TestTrialUsesOneCalendarMonth(t *testing.T) {
	start := UTCDate(2026, 1, 31)
	snap := Snapshot{TrialStartDate: ptr(start)}

	before := Evaluate(snap, nil, UTCDate(2026, 2, 27))
	if before.Reason != ReasonTrial {
		t.Fatalf("reason = %s, want trial", before.Reason)
	}
	if before.TrialEndsAt == nil || !before.TrialEndsAt.Equal(UTCDate(2026, 2, 28)) {
		t.Fatalf("trialEndsAt = %v, want 2026-02-28", before.TrialEndsAt)
	}
	if !before.CanUseFeature(FeatureTunnelRouting) {
		t.Fatal("expected tunnelRouting unlocked during trial")
	}

	at := Evaluate(snap, nil, UTCDate(2026, 2, 28))
	if at.Reason != ReasonLocked || at.CanUseApp() {
		t.Fatalf("at expiry reason = %s canUse = %v, want locked", at.Reason, at.CanUseApp())
	}
}

func TestTrialEndDateClampsToTargetMonthLastDay(t *testing.T) {
	if got := TrialEndDate(UTCDate(2025, 12, 31)); !got.Equal(UTCDate(2026, 1, 31)) {
		t.Fatalf("Dec 31 2025 +1mo = %v, want 2026-01-31", got)
	}
	if got := TrialEndDate(UTCDate(2023, 12, 31)); !got.Equal(UTCDate(2024, 1, 31)) {
		t.Fatalf("Dec 31 2023 +1mo = %v, want 2024-01-31", got)
	}
	if got := TrialEndDate(UTCDate(2026, 1, 31)); !got.Equal(UTCDate(2026, 2, 28)) {
		t.Fatalf("Jan 31 2026 +1mo = %v, want 2026-02-28", got)
	}
}

func TestExpiredTrialLocksPremiumFeatures(t *testing.T) {
	snap := Snapshot{TrialStartDate: ptr(UTCDate(2026, 6, 3))}
	d := Evaluate(snap, nil, UTCDate(2026, 8, 4))
	if d.Reason != ReasonLocked || d.CanUseApp() {
		t.Fatalf("reason = %s canUse = %v, want locked", d.Reason, d.CanUseApp())
	}
	if d.CanUseFeature(FeatureTunnelRouting) || d.CanUseFeature(FeatureRoutingRules) {
		t.Fatal("expected features locked after trial")
	}
}

func TestLicenseRemainsUsableWithoutRecentVerification(t *testing.T) {
	snap := Snapshot{
		Transactions:   []Transaction{lifetimeTx(UTCDate(2026, 6, 3))},
		LastVerifiedAt: ptr(UTCDate(2026, 6, 10)),
	}
	d := Evaluate(snap, nil, UTCDate(2028, 6, 18))
	if d.Reason != ReasonLifetime || !d.CanUseApp() {
		t.Fatalf("reason = %s, want lifetime", d.Reason)
	}
	if d.UpdateCutoffDate == nil || !d.UpdateCutoffDate.Equal(UTCDate(2027, 6, 3)) {
		t.Fatalf("cutoff = %v, want 2027-06-03", d.UpdateCutoffDate)
	}
	if !d.CanUseFeature(FeatureTunnelRouting) {
		t.Fatal("expected tunnelRouting unlocked")
	}
}

func TestRecentVerificationFailureUsesOfflineGrace(t *testing.T) {
	included := Feature{ID: FeatureWidgets, DisplayName: "Included Widgets", ReleaseDate: UTCDate(2027, 6, 3)}
	later := Feature{ID: FeatureActivityInspection, DisplayName: "Later Inspection", ReleaseDate: UTCDate(2027, 6, 4)}
	snap := Snapshot{
		Transactions:             []Transaction{lifetimeTx(UTCDate(2026, 6, 3))},
		LastVerifiedAt:           ptr(UTCDate(2026, 7, 1)),
		LastVerificationFailedAt: ptr(UTCDate(2026, 7, 2)),
	}
	d := Evaluate(snap, []Feature{included, later}, UTCDate(2026, 7, 5))
	if d.Reason != ReasonOfflineGrace || !d.IsOfflineGraceActive() {
		t.Fatalf("reason = %s, want offlineGrace", d.Reason)
	}
	if d.OfflineGraceEndsAt == nil || !d.OfflineGraceEndsAt.Equal(UTCDate(2026, 7, 9)) {
		t.Fatalf("graceEnd = %v, want 2026-07-09", d.OfflineGraceEndsAt)
	}
	if !d.CanUseFeature(FeatureWidgets) || d.CanUseFeature(FeatureActivityInspection) {
		t.Fatal("grace should gate features by cutoff")
	}
}

func TestOfflinePaidUseKeepsPurchasedReleasesEnabled(t *testing.T) {
	included := Feature{ID: FeatureWidgets, DisplayName: "Included Widgets", ReleaseDate: UTCDate(2027, 6, 3)}
	later := Feature{ID: FeatureActivityInspection, DisplayName: "Later Inspection", ReleaseDate: UTCDate(2027, 6, 4)}
	snap := Snapshot{
		Transactions:             []Transaction{lifetimeTx(UTCDate(2026, 6, 3))},
		LastVerifiedAt:           ptr(UTCDate(2026, 6, 10)),
		LastVerificationFailedAt: ptr(UTCDate(2026, 6, 12)),
	}
	// lastVerified before failed, but grace expired long ago -> lifetime.
	d := Evaluate(snap, []Feature{included, later}, UTCDate(2028, 6, 14))
	if d.Reason != ReasonLifetime || d.IsOfflineGraceActive() || d.OfflineGraceEndsAt != nil {
		t.Fatalf("reason = %s grace = %v, want lifetime no grace", d.Reason, d.OfflineGraceEndsAt)
	}
	if !d.CanUseFeature(FeatureWidgets) || d.CanUseFeature(FeatureActivityInspection) {
		t.Fatal("cutoff gating wrong")
	}
}

func TestRevokedLicenseDoesNotUnlock(t *testing.T) {
	snap := Snapshot{
		Transactions: []Transaction{{
			ProductID:      LifetimeUnlockProductID,
			PurchaseDate:   UTCDate(2026, 6, 3),
			RevocationDate: ptr(UTCDate(2026, 7, 1)),
		}},
		LastVerifiedAt: ptr(UTCDate(2026, 7, 1)),
	}
	d := Evaluate(snap, nil, UTCDate(2026, 7, 2))
	if d.Reason != ReasonLocked || d.HasLifetimeUnlock {
		t.Fatalf("reason = %s hasLifetime = %v, want locked", d.Reason, d.HasLifetimeUnlock)
	}
}

func TestPaidUpdatesExtendFeatureWindow(t *testing.T) {
	snap := Snapshot{
		Transactions: []Transaction{
			lifetimeTx(UTCDate(2026, 6, 3)),
			{ProductID: FeatureUpdateProductID, PurchaseDate: UTCDate(2027, 8, 1)},
		},
		LastVerifiedAt: ptr(UTCDate(2027, 8, 1)),
	}
	future := Feature{ID: FeatureWidgets, DisplayName: "Future Widgets", ReleaseDate: UTCDate(2028, 7, 31)}
	d := Evaluate(snap, []Feature{future}, UTCDate(2027, 8, 2))
	if d.UpdateCutoffDate == nil || !d.UpdateCutoffDate.Equal(UTCDate(2028, 8, 1)) {
		t.Fatalf("cutoff = %v, want 2028-08-01", d.UpdateCutoffDate)
	}
	if !d.CanUseFeature(FeatureWidgets) {
		t.Fatal("expected future widgets unlocked")
	}
}

func TestPaidUpdateBeforeCutoffExtendsFromCutoff(t *testing.T) {
	purchase := UTCDate(2026, 6, 3)
	txs := []Transaction{
		lifetimeTx(purchase),
		{ProductID: FeatureUpdateProductID, PurchaseDate: UTCDate(2027, 1, 15)},
	}
	got := UpdateCutoffDate(purchase, txs)
	if !got.Equal(UTCDate(2028, 6, 3)) {
		t.Fatalf("cutoff = %v, want 2028-06-03", got)
	}
}

func TestMultiplePaidUpdateYearsGateByReleaseDate(t *testing.T) {
	snap := Snapshot{
		Transactions: []Transaction{
			lifetimeTx(UTCDate(2026, 6, 3)),
			{ProductID: FeatureUpdateProductID, PurchaseDate: UTCDate(2027, 8, 1)},
			{ProductID: FeatureUpdateProductID, PurchaseDate: UTCDate(2028, 8, 15)},
		},
		LastVerifiedAt: ptr(UTCDate(2028, 8, 15)),
	}
	first := Feature{ID: FeatureWidgets, DisplayName: "First", ReleaseDate: UTCDate(2028, 8, 1)}
	final := Feature{ID: FeatureRoutingRules, DisplayName: "Final", ReleaseDate: UTCDate(2029, 8, 15)}
	later := Feature{ID: FeatureActivityInspection, DisplayName: "Later", ReleaseDate: UTCDate(2029, 8, 16)}
	d := Evaluate(snap, []Feature{first, final, later}, UTCDate(2028, 8, 16))
	if d.UpdateCutoffDate == nil || !d.UpdateCutoffDate.Equal(UTCDate(2029, 8, 15)) {
		t.Fatalf("cutoff = %v, want 2029-08-15", d.UpdateCutoffDate)
	}
	if !d.CanUseFeature(FeatureWidgets) || !d.CanUseFeature(FeatureRoutingRules) || d.CanUseFeature(FeatureActivityInspection) {
		t.Fatal("multi-year gating wrong")
	}
}

func TestRefundedPaidUpdateDoesNotExtend(t *testing.T) {
	snap := Snapshot{
		Transactions: []Transaction{
			lifetimeTx(UTCDate(2026, 6, 3)),
			{ProductID: FeatureUpdateProductID, PurchaseDate: UTCDate(2027, 8, 1), RevocationDate: ptr(UTCDate(2027, 8, 10))},
		},
		LastVerifiedAt: ptr(UTCDate(2027, 8, 10)),
	}
	future := Feature{ID: FeatureWidgets, DisplayName: "Future", ReleaseDate: UTCDate(2028, 7, 31)}
	d := Evaluate(snap, []Feature{future}, UTCDate(2027, 8, 11))
	if d.UpdateCutoffDate == nil || !d.UpdateCutoffDate.Equal(UTCDate(2027, 6, 3)) {
		t.Fatalf("cutoff = %v, want 2027-06-03", d.UpdateCutoffDate)
	}
	if d.CanUseFeature(FeatureWidgets) {
		t.Fatal("refunded update should not unlock future feature")
	}
}

func TestPaidUpdateWithoutLicenseDoesNotUnlock(t *testing.T) {
	snap := Snapshot{
		Transactions:   []Transaction{{ProductID: FeatureUpdateProductID, PurchaseDate: UTCDate(2027, 8, 1)}},
		LastVerifiedAt: ptr(UTCDate(2027, 8, 1)),
	}
	d := Evaluate(snap, nil, UTCDate(2027, 8, 2))
	if d.Reason != ReasonLocked || d.CanUseApp() {
		t.Fatalf("reason = %s, want locked", d.Reason)
	}
}

func TestUpdatePolicyDatedReleasesThroughCutoff(t *testing.T) {
	d := Evaluate(Snapshot{Transactions: []Transaction{lifetimeTx(UTCDate(2026, 6, 3))}}, nil, UTCDate(2028, 1, 1))
	if !CanInstallUpdate(d, ptr(UTCDate(2027, 6, 3)), time.Time{}) {
		t.Fatal("release on cutoff should install")
	}
	if CanInstallUpdate(d, ptr(UTCDate(2027, 6, 4)), time.Time{}) {
		t.Fatal("release after cutoff should not install")
	}
}

func TestUpdatePolicyFailsClosedForUndatedRelease(t *testing.T) {
	d := Evaluate(Snapshot{Transactions: []Transaction{lifetimeTx(UTCDate(2026, 6, 3))}}, nil, UTCDate(2028, 1, 1))
	if !CanInstallUpdate(d, nil, UTCDate(2027, 6, 3)) {
		t.Fatal("undated release at cutoff should install")
	}
	if CanInstallUpdate(d, nil, UTCDate(2027, 6, 4)) {
		t.Fatal("undated release after cutoff should not install")
	}
}

func TestUpdatePolicyTrialAndLocked(t *testing.T) {
	active := Evaluate(Snapshot{TrialStartDate: ptr(UTCDate(2026, 6, 3))}, nil, UTCDate(2026, 7, 2))
	expired := Evaluate(Snapshot{TrialStartDate: ptr(UTCDate(2026, 6, 3))}, nil, UTCDate(2026, 7, 3))
	if !CanInstallUpdate(active, nil, time.Time{}) {
		t.Fatal("active trial should install")
	}
	if CanInstallUpdate(expired, ptr(UTCDate(2026, 6, 4)), time.Time{}) {
		t.Fatal("locked trial should not install")
	}
}

func TestPaidUpdatePolicyCopyLanguage(t *testing.T) {
	copy := PaidUpdatePolicyCopy(UTCDate(2027, 6, 3))
	for _, want := range []string{
		"The ClambHook license includes all updates released through ",
		"Versions released during that window remain usable.",
		"including critical, bug, and security updates",
		"USD 9.99 update-year renewal",
	} {
		if !strings.Contains(copy, want) {
			t.Fatalf("copy missing %q: %s", want, copy)
		}
	}
}

func TestProductStatesActiveTrial(t *testing.T) {
	d := Evaluate(Snapshot{TrialStartDate: ptr(UTCDate(2026, 6, 3))}, nil, UTCDate(2026, 7, 1))
	trial := findState(t, ProductStates(d, nil), ProductStateTrial)
	if trial.Title != "One-calendar-month trial" || !trial.IsActive {
		t.Fatalf("trial state wrong: %+v", trial)
	}
	if !strings.Contains(trial.Detail, "Trial ends") || !strings.Contains(trial.Detail, "2026") {
		t.Fatalf("trial detail wrong: %s", trial.Detail)
	}
}

func TestProductStatesPaidUpdateWindowDate(t *testing.T) {
	d := Evaluate(Snapshot{Transactions: []Transaction{lifetimeTx(UTCDate(2026, 6, 3))}}, nil, UTCDate(2026, 7, 1))
	win := findState(t, ProductStates(d, nil), ProductStatePaidUpdateWindow)
	if !win.IsActive || !strings.HasPrefix(win.Title, "Included updates through ") || !strings.Contains(win.Title, "2027") {
		t.Fatalf("paid window state wrong: %+v", win)
	}
}

func TestProductStatesAlwaysShowLockedRow(t *testing.T) {
	d := Evaluate(Snapshot{Transactions: []Transaction{lifetimeTx(UTCDate(2026, 6, 3))}}, nil, UTCDate(2026, 7, 1))
	locked := findState(t, ProductStates(d, nil), ProductStateNewFeaturesLocked)
	if locked.Title != "Later updates require renewal" || locked.IsActive {
		t.Fatalf("locked row wrong: %+v", locked)
	}
	if !strings.Contains(locked.Detail, "All updates released after the cutoff") {
		t.Fatalf("locked detail wrong: %s", locked.Detail)
	}
}

func TestProductStatesMarkFutureFeaturesLocked(t *testing.T) {
	d := Evaluate(Snapshot{Transactions: []Transaction{lifetimeTx(UTCDate(2026, 6, 3))}}, nil, UTCDate(2026, 7, 1))
	future := Feature{ID: FeatureWidgets, DisplayName: "Future Widgets", ReleaseDate: UTCDate(2027, 6, 4)}
	locked := findState(t, ProductStates(d, []Feature{future}), ProductStateNewFeaturesLocked)
	if !locked.IsActive || !strings.Contains(locked.Detail, "Future Widgets") {
		t.Fatalf("future locked row wrong: %+v", locked)
	}
}

func TestExpiredTrialRecoveryState(t *testing.T) {
	d := Evaluate(Snapshot{TrialStartDate: ptr(UTCDate(2026, 6, 3))}, nil, UTCDate(2026, 8, 4))
	state := ExpiredTrialState(d)
	if state == nil || state.Kind != RecoveryExpiredTrial || state.PrimaryAction != ActionBuyLicense {
		t.Fatalf("expired trial state wrong: %+v", state)
	}
	if ExpiredTrialState(Evaluate(Snapshot{TrialStartDate: ptr(UTCDate(2026, 6, 3))}, nil, UTCDate(2026, 6, 10))) != nil {
		t.Fatal("active trial should have no expired banner")
	}
}

func TestLicenseExpiredForUpdatesRecoveryState(t *testing.T) {
	d := Evaluate(Snapshot{Transactions: []Transaction{lifetimeTx(UTCDate(2026, 6, 3))}}, nil, UTCDate(2027, 6, 4))
	after := LicenseExpiredForUpdatesState(d, ptr(UTCDate(2027, 6, 4)), UTCDate(2027, 6, 4))
	if after == nil || after.PrimaryAction != ActionRenewUpdates {
		t.Fatalf("expected renew banner, got %+v", after)
	}
	if !strings.Contains(after.Message, "after your included update window ended") {
		t.Fatalf("message wrong: %s", after.Message)
	}
	before := LicenseExpiredForUpdatesState(d, ptr(UTCDate(2027, 6, 3)), UTCDate(2027, 6, 3))
	if before != nil {
		t.Fatal("release on cutoff should not raise banner")
	}
}

func TestDeviceStateHonorsTenActiveLimit(t *testing.T) {
	devices := make([]Device, 0, 10)
	for i := 1; i <= 10; i++ {
		devices = append(devices, Device{DeviceID: "d", InstallID: "i", ActivatedAt: UTCDate(2026, 6, 3)})
	}
	s := DeviceState{CurrentInstallID: "install-11", MaxActiveDevices: MaxActiveDevices, Devices: devices}.Normalized()
	if s.MaxActiveDevices != 10 || s.ActiveDeviceCount() != 10 || s.RemainingActivations() != 0 {
		t.Fatalf("limit wrong: max=%d active=%d rem=%d", s.MaxActiveDevices, s.ActiveDeviceCount(), s.RemainingActivations())
	}
	if s.CanActivateCurrentDevice() || s.CanReactivateCurrentDevice() {
		t.Fatal("no seat should be available at limit")
	}
}

func TestDeviceStateCannotRaiseLimitAboveTen(t *testing.T) {
	var s DeviceState
	if err := json.Unmarshal([]byte(`{"current_install_id":"install-11","max_active_devices":25,"devices":[]}`), &s); err != nil {
		t.Fatal(err)
	}
	if got := s.Normalized().MaxActiveDevices; got != 10 {
		t.Fatalf("max = %d, want 10", got)
	}
}

func TestCommercialTerms(t *testing.T) {
	if LicensePriceUSD != "99.99" || PaidUpdatePriceUSD != "9.99" || IncludedUpdateYears != 1 || MaxActiveDevices != 10 {
		t.Fatal("commercial terms drifted from contract")
	}
}

func TestAcceptedProvidersAreExactlyCreemAndNOWPayments(t *testing.T) {
	accepted := AcceptedPurchaseProviders()
	if len(accepted) != 2 || accepted[0] != ProviderCreem || accepted[1] != ProviderNOWPayments {
		t.Fatalf("accepted providers = %+v", accepted)
	}
	if accepted[0].DisplayName() != "Creem" || accepted[1].DisplayName() != "NOWPayments" {
		t.Fatal("provider display names wrong")
	}
	if !ProviderCreem.IsAccepted() || !ProviderNOWPayments.IsAccepted() {
		t.Fatal("creem/nowpayments must be accepted")
	}
}

func TestUnsupportedProvidersDecodeButAreNotAccepted(t *testing.T) {
	for _, raw := range []string{"manual", "paypal", "future-provider"} {
		var p PaymentProvider
		if err := json.Unmarshal([]byte(`"`+raw+`"`), &p); err != nil {
			t.Fatal(err)
		}
		if p.Raw != raw || p.IsAccepted() {
			t.Fatalf("provider %q decoded wrong: %+v accepted=%v", raw, p, p.IsAccepted())
		}
	}
}

func TestActiveCurrentDeviceCanRemainActiveAtLimit(t *testing.T) {
	s := DeviceState{
		CurrentInstallID: "install-1",
		CurrentDeviceID:  "device-1",
		MaxActiveDevices: MaxActiveDevices,
		Devices: []Device{
			{DeviceID: "device-1", InstallID: "install-1", ActivatedAt: UTCDate(2026, 6, 3)},
			{DeviceID: "device-2", InstallID: "install-2", ActivatedAt: UTCDate(2026, 6, 3)},
			{DeviceID: "device-3", InstallID: "install-3", ActivatedAt: UTCDate(2026, 6, 3)},
			{DeviceID: "device-4", InstallID: "install-4", ActivatedAt: UTCDate(2026, 6, 3)},
		},
	}
	if !s.IsCurrentDeviceActive() || !s.CanActivateCurrentDevice() || !s.CanTransferCurrentDevice() {
		t.Fatal("active current device semantics wrong")
	}
}

func TestReactivationRequiresAvailableSeat(t *testing.T) {
	deactivated := Device{DeviceID: "device-1", InstallID: "install-1", ActivatedAt: UTCDate(2026, 6, 3), DeactivatedAt: ptr(UTCDate(2026, 7, 1))}
	full := DeviceState{CurrentInstallID: "install-1", CurrentDeviceID: "device-1", MaxActiveDevices: MaxActiveDevices, Devices: []Device{deactivated}}
	for i := 2; i <= 11; i++ {
		full.Devices = append(full.Devices, Device{DeviceID: "d", InstallID: "i", ActivatedAt: UTCDate(2026, 6, 3)})
	}
	available := DeviceState{CurrentInstallID: "install-1", CurrentDeviceID: "device-1", MaxActiveDevices: MaxActiveDevices, Devices: []Device{deactivated}}
	for i := 2; i <= 10; i++ {
		available.Devices = append(available.Devices, Device{DeviceID: "d", InstallID: "i", ActivatedAt: UTCDate(2026, 6, 3)})
	}
	if full.CanReactivateCurrentDevice() {
		t.Fatal("no seat: reactivation should be blocked")
	}
	if !available.CanReactivateCurrentDevice() {
		t.Fatal("open seat: reactivation should be allowed")
	}
}

func TestDeviceStateJSONRoundTrip(t *testing.T) {
	s := DeviceState{
		CurrentInstallID: "install-1",
		CurrentDeviceID:  "device-1",
		MaxActiveDevices: MaxActiveDevices,
		Devices:          []Device{{DeviceID: "device-1", InstallID: "install-1", ActivatedAt: UTCDate(2026, 6, 3)}},
		PaymentProvider:  &ProviderCreem,
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var loaded DeviceState
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.PaymentProvider == nil || loaded.PaymentProvider.Raw != "creem" {
		t.Fatalf("provider round trip wrong: %+v", loaded.PaymentProvider)
	}
	if loaded.CurrentDevice() == nil || loaded.CurrentDevice().DeviceID != "device-1" {
		t.Fatal("current device round trip wrong")
	}
}

func TestSnapshotDecodesCamelCaseTransactions(t *testing.T) {
	jsonBody := `{"trialStartDate":null,"transactions":[{"productID":"org.jpfchang.clambhook.unlock.lifetime","purchaseDate":"2026-06-03T00:00:00Z"}],"lastVerifiedAt":null,"lastVerificationFailedAt":null,"cachedAt":"2026-06-03T00:00:00Z"}`
	var s Snapshot
	if err := json.Unmarshal([]byte(jsonBody), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Transactions) != 1 || s.Transactions[0].ProductID != LifetimeUnlockProductID {
		t.Fatalf("snapshot decode wrong: %+v", s.Transactions)
	}
	if s.Transactions[0].Kind() != ProductLifetimeUnlock {
		t.Fatal("product kind resolution wrong")
	}
}

func TestEnsureTrialStartSeedsAndPreserves(t *testing.T) {
	now := UTCDate(2026, 7, 15)
	seeded := EnsureTrialStart(Snapshot{}, now)
	if seeded.TrialStartDate == nil || !seeded.TrialStartDate.Equal(now) {
		t.Fatalf("expected seeded trial start = now, got %v", seeded.TrialStartDate)
	}
	original := UTCDate(2026, 6, 3)
	preserved := EnsureTrialStart(Snapshot{TrialStartDate: ptr(original)}, now)
	if preserved.TrialStartDate == nil || !preserved.TrialStartDate.Equal(original) {
		t.Fatalf("expected preserved trial start, got %v", preserved.TrialStartDate)
	}
}

func findState(t *testing.T, states []ProductState, kind ProductStateKind) ProductState {
	t.Helper()
	for _, s := range states {
		if s.Kind == kind {
			return s
		}
	}
	t.Fatalf("state %s not found", kind)
	return ProductState{}
}
