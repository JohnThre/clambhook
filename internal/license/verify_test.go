package license

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// Test-only Ed25519 keypair (matches tests/unit/clambhook-grant-signing.test.ts
// in the swiphtgroup.com repo). Both halves are committed for tests only; they
// are NOT the production key.
const (
	testPrivateKeyB64 = "MC4CAQAwBQYDK2VwBCIEIOr839ef5i0O7VYR8Ax83UctlcSpe+BkETpCzUq2od+x"
	testPublicKeyB64  = "6EIR6S1pCEC2HLZijYwnbP7rrXh3LtqONaDqE6iPhPM="
	testKeyID         = "clambhook-grant-test-v1"
	testBuildSecret   = "clambhook-test-build-secret"
)

func mustTestSign(t *testing.T, msg string) string {
	t.Helper()
	pkcs8, err := base64.StdEncoding.DecodeString(testPrivateKeyB64)
	if err != nil {
		t.Fatal(err)
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(pkcs8)
	if err != nil {
		t.Fatal(err)
	}
	key := keyAny.(ed25519.PrivateKey)
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(msg)))
}

func sampleGrant() ServerGrant {
	return ServerGrant{
		Version:           1,
		IssuedAt:          UTCDate(2026, 6, 10),
		ExpiresAt:         UTCDate(2026, 6, 17),
		Reason:            ReasonLifetime,
		HasLifetimeUnlock: true,
		UpdateCutoffDate:  ptrTimeUTC(UTCDate(2027, 6, 10)),
		Transactions:      []Transaction{},
	}
}

func ptrTimeUTC(t time.Time) *time.Time { return &t }

func TestCanonicalGrantMatchesPinnedSample(t *testing.T) {
	if got := CanonicalizeGrant(sampleGrant()); got != CANONICAL_GRANT_SAMPLE {
		t.Fatalf("canonical grant diverged from the pinned sample (Go/TS must match):\n got: %s\nwant: %s", got, CANONICAL_GRANT_SAMPLE)
	}
}

func TestCanonicalGrantWithTransactions(t *testing.T) {
	g := sampleGrant()
	g.Transactions = []Transaction{
		{ProductID: LifetimeUnlockProductID, PurchaseDate: UTCDate(2026, 1, 1)},
		{ProductID: FeatureUpdateProductID, PurchaseDate: UTCDate(2026, 2, 1), RevocationDate: ptrTimeUTC(UTCDate(2026, 3, 1))},
	}
	want := `{"version":1,"issued_at":"2026-06-10T00:00:00Z","expires_at":"2026-06-17T00:00:00Z","reason":"lifetime","trial_start_date":null,"trial_ends_at":null,"has_lifetime_unlock":true,"update_cutoff_date":"2027-06-10T00:00:00Z","transactions":[{"productID":"org.jpfchang.clambhook.unlock.lifetime","purchaseDate":"2026-01-01T00:00:00Z","revocationDate":null},{"productID":"org.jpfchang.clambhook.feature_update","purchaseDate":"2026-02-01T00:00:00Z","revocationDate":"2026-03-01T00:00:00Z"}]}`
	if got := CanonicalizeGrant(g); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestVerifyGrantLegacyUnsignedIsAccepted(t *testing.T) {
	g := sampleGrant() // KeyID == "" → legacy
	if err := VerifyGrant(g); err != nil {
		t.Fatalf("legacy grant should verify, got %v", err)
	}
}

func TestVerifyGrantRoundTripAndFailures(t *testing.T) {
	restore := SetVerifyingKeyForTest(testKeyID, testPublicKeyB64)
	defer restore()

	g := sampleGrant()
	g.KeyID = testKeyID
	g.Signature = mustTestSign(t, CanonicalizeGrant(g))
	if err := VerifyGrant(g); err != nil {
		t.Fatalf("valid signed grant should verify: %v", err)
	}

	// Tampered grant fails.
	tampered := g
	tampered.HasLifetimeUnlock = false
	if err := VerifyGrant(tampered); err == nil {
		t.Fatal("tampered grant should fail verification")
	}

	// Wrong key_id fails.
	other := g
	other.KeyID = "unknown-key"
	if err := VerifyGrant(other); err == nil {
		t.Fatal("unknown key_id should fail")
	}

	// Empty signature with a key_id fails.
	empty := g
	empty.Signature = ""
	if err := VerifyGrant(empty); err == nil {
		t.Fatal("key_id with empty signature should fail")
	}

	// Garbage signature fails.
	bad := g
	bad.Signature = "not-base64!!"
	if err := VerifyGrant(bad); err == nil {
		t.Fatal("garbage signature should fail")
	}
}

func TestVerifyBanMarker(t *testing.T) {
	restore := SetVerifyingKeyForTest(testKeyID, testPublicKeyB64)
	defer restore()

	mk := func(install string) BanMarker {
		m := BanMarker{
			BanID:     "clhb_1",
			Reason:    "cracked",
			Source:    "verification",
			BannedAt:  UTCDate(2026, 6, 10),
			InstallID: install,
			KeyID:     testKeyID,
		}
		m.Signature = mustTestSign(t, CanonicalizeBan(m))
		return m
	}

	m := mk("install-1")
	if err := VerifyBanMarker(m, "install-1"); err != nil {
		t.Fatalf("valid ban verdict should verify: %v", err)
	}
	// Bound to a different install_id → rejected.
	if err := VerifyBanMarker(m, "install-2"); err == nil {
		t.Fatal("ban for a different install_id should be rejected")
	}
	// Tampered reason → rejected.
	tampered := m
	tampered.Reason = "refund"
	if err := VerifyBanMarker(tampered, "install-1"); err == nil {
		t.Fatal("tampered ban verdict should be rejected")
	}
	// No key_id → rejected.
	unsigned := m
	unsigned.KeyID = ""
	if err := VerifyBanMarker(unsigned, "install-1"); err == nil {
		t.Fatal("unsigned ban verdict should be rejected")
	}
}

func TestBuildAttestationTokenDeterministic(t *testing.T) {
	a := BuildAttestationToken(testKeyID, "install-1", "abc123", nil, "n", 1700000000000, testBuildSecret)
	b := BuildAttestationToken(testKeyID, "install-1", "abc123", nil, "n", 1700000000000, testBuildSecret)
	if a != b {
		t.Fatal("build token must be deterministic for identical inputs")
	}
	// Different secret → different token.
	c := BuildAttestationToken(testKeyID, "install-1", "abc123", nil, "n", 1700000000000, "other-secret")
	if a == c {
		t.Fatal("different secret must yield a different token")
	}
	// Canonical attestation shape pin (matches the TS canonicalizeAttestation).
	want := `{"key_id":"k","install_id":"i","binary_hash":"h","code_sign_identity":null,"nonce":"n","timestamp":7}`
	if got := CanonicalizeAttestation("k", "i", "h", nil, "n", 7); got != want {
		t.Fatalf("attestation canonical diverged:\n got: %s\nwant: %s", got, want)
	}
}

func TestEvaluateStateBanOverridesEverything(t *testing.T) {
	restore := SetVerifyingKeyForTest(testKeyID, testPublicKeyB64)
	defer restore()

	now := UTCDate(2026, 7, 1)
	m := BanMarker{
		BanID: "clhb_1", Reason: "cracked", Source: "verification",
		BannedAt: now, InstallID: "install-1", KeyID: testKeyID,
	}
	m.Signature = mustTestSign(t, CanonicalizeBan(m))

	// Even with a lifetime snapshot + signed grant, a valid active ban ⇒ banned.
	state := LicenseState{
		InstallID: "install-1",
		BanMarker: &m,
		Snapshot: Snapshot{
			Transactions: []Transaction{{ProductID: LifetimeUnlockProductID, PurchaseDate: UTCDate(2026, 6, 3)}},
		},
	}
	d := EvaluateState(state, nil, now.Add(time.Hour))
	if d.Reason != ReasonBanned || d.CanUseApp() {
		t.Fatalf("ban must override; got %+v", d)
	}
	if !d.IsBanned || d.BanReason != "cracked" {
		t.Fatalf("ban fields not populated: %+v", d)
	}
	if d.SupportEmail == "" || d.DisputeURL == "" {
		t.Fatalf("ban decision should default support/dispute: %+v", d)
	}
}

func TestEvaluateStateExpiredOrTamperedBanIsIgnored(t *testing.T) {
	restore := SetVerifyingKeyForTest(testKeyID, testPublicKeyB64)
	defer restore()

	now := UTCDate(2026, 7, 1)
	// Expired ban: IsActive is false, so it must not lock.
	expiry := UTCDate(2026, 6, 1)
	expired := BanMarker{
		BanID: "clhb_2", Reason: "cracked", Source: "verification",
		BannedAt: UTCDate(2026, 5, 1), ExpiresAt: &expiry, InstallID: "install-1", KeyID: testKeyID,
	}
	expired.Signature = mustTestSign(t, CanonicalizeBan(expired))
	state := LicenseState{
		InstallID: "install-1",
		BanMarker: &expired,
		Snapshot:  Snapshot{Transactions: []Transaction{{ProductID: LifetimeUnlockProductID, PurchaseDate: UTCDate(2026, 6, 3)}}},
	}
	d := EvaluateState(state, nil, now)
	if d.Reason == ReasonBanned {
		t.Fatalf("expired ban should not lock; got %+v", d)
	}
	// Tampered (bad signature) ban is ignored.
	tampered := expired
	tampered.ExpiresAt = nil
	tampered.Signature = base64.StdEncoding.EncodeToString([]byte("tampered-sig"))
	state.BanMarker = &tampered
	d2 := EvaluateState(state, nil, now)
	if d2.Reason == ReasonBanned {
		t.Fatalf("tampered ban should be ignored, not lock; got %+v", d2)
	}
}

func TestEvaluateStateLifetimeRequiresSignedGrant(t *testing.T) {
	now := UTCDate(2026, 7, 1)
	snap := Snapshot{Transactions: []Transaction{{ProductID: LifetimeUnlockProductID, PurchaseDate: UTCDate(2026, 6, 3)}}}

	// No grant, no legacy ⇒ lifetime demoted to locked (anti-forgery).
	d := EvaluateState(LicenseState{Snapshot: snap}, nil, now)
	if d.Reason != ReasonLocked {
		t.Fatalf("lifetime without a signed grant should be locked; got %s", d.Reason)
	}

	// Legacy affordance ⇒ lifetime honored (upgrade migration).
	d = EvaluateState(LicenseState{Snapshot: snap, LegacyUnsignedOK: true}, nil, now)
	if d.Reason != ReasonLifetime {
		t.Fatalf("legacy lifetime should be honored; got %s", d.Reason)
	}

	// Validly signed grant ⇒ lifetime honored.
	restore := SetVerifyingKeyForTest(testKeyID, testPublicKeyB64)
	defer restore()
	g := sampleGrant()
	g.KeyID = testKeyID
	g.Signature = mustTestSign(t, CanonicalizeGrant(g))
	d = EvaluateState(LicenseState{Snapshot: snap, Grant: &g}, nil, now)
	if d.Reason != ReasonLifetime {
		t.Fatalf("signed grant lifetime should be honored; got %s", d.Reason)
	}

	// Unsigned grant with a key_id (forged) ⇒ locked.
	bad := g
	bad.Signature = ""
	d = EvaluateState(LicenseState{Snapshot: snap, Grant: &bad}, nil, now)
	if d.Reason != ReasonLocked {
		t.Fatalf("forged unsigned grant should not unlock lifetime; got %s", d.Reason)
	}
}

func TestEvaluateStateTrialIsLocal(t *testing.T) {
	now := UTCDate(2026, 6, 10)
	snap := Snapshot{TrialStartDate: ptrTimeUTC(UTCDate(2026, 6, 3))}
	// Trial does not require a signed grant.
	d := EvaluateState(LicenseState{Snapshot: snap}, nil, now)
	if d.Reason != ReasonTrial {
		t.Fatalf("trial should be honored without a grant; got %s", d.Reason)
	}
	// Ban overrides trial.
	restore := SetVerifyingKeyForTest(testKeyID, testPublicKeyB64)
	defer restore()
	m := BanMarker{BanID: "b", Reason: "cracked", Source: "verification", BannedAt: now, InstallID: "install-1", KeyID: testKeyID}
	m.Signature = mustTestSign(t, CanonicalizeBan(m))
	d = EvaluateState(LicenseState{Snapshot: snap, InstallID: "install-1", BanMarker: &m}, nil, now)
	if d.Reason != ReasonBanned {
		t.Fatalf("ban must override trial; got %s", d.Reason)
	}
}

// guard against accidentally compiling in test helpers that callers misuse.
var _ = strings.TrimSpace
