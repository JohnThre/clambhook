package licenseverify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/license"
)

func mustTempState(t *testing.T, initial license.LicenseState) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "license.json")
	if initial.InstallID != "" || initial.Grant != nil || initial.BanMarker != nil || initial.LegacyUnsignedOK {
		b, _ := json.Marshal(initial)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func stateFromFile(t *testing.T, p string) license.LicenseState {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var s license.LicenseState
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func newTestVerifier(t *testing.T, cfg Config) *Verifier {
	cfg.Now = func() time.Time { return license.UTCDate(2026, 7, 1) }
	cfg.SelfAttestation = func() (string, *string) { return "binaryhash", nil }
	return New(cfg)
}

func genuineAttestResult() string {
	grant := &license.ServerGrant{
		Version: 1, IssuedAt: license.UTCDate(2026, 7, 1), ExpiresAt: license.UTCDate(2027, 7, 1),
		Reason: license.ReasonLifetime, HasLifetimeUnlock: true,
		UpdateCutoffDate: ptr(license.UTCDate(2027, 7, 1)),
		Transactions:     []license.Transaction{{ProductID: license.LifetimeUnlockProductID, PurchaseDate: license.UTCDate(2026, 6, 3)}},
	}
	snap := license.Snapshot{
		Transactions: grant.Transactions,
	}
	res := licensebridgeAttestResult{
		Genuine:     true,
		Grant:       grant,
		Snapshot:    snap,
		DeviceState: license.DeviceState{CurrentDeviceID: "device-1"},
		Decision:    license.Decision{Reason: license.ReasonLifetime, HasLifetimeUnlock: true},
	}
	b, _ := json.Marshal(res)
	return string(b)
}

func bannedAttestResult() string {
	m := &license.BanMarker{BanID: "clhb_1", Reason: "cracked", Source: "verification", BannedAt: license.UTCDate(2026, 7, 1), InstallID: "install-1"}
	res := licensebridgeAttestResult{
		Banned: true, BanMarker: m,
		Decision: license.Decision{Reason: license.ReasonBanned, IsBanned: true, BanReason: "cracked"},
	}
	b, _ := json.Marshal(res)
	return string(b)
}

func ptr(t time.Time) *time.Time { return &t }

func TestAttestOnceGenuinePersistsGrantAndClearsLegacy(t *testing.T) {
	path := mustTempState(t, license.LicenseState{
		InstallID:        "install-1",
		LegacyUnsignedOK: true,
		Snapshot:         license.Snapshot{Transactions: []license.Transaction{{ProductID: license.LifetimeUnlockProductID, PurchaseDate: license.UTCDate(2026, 6, 3)}}},
	})
	var halt int32
	v := newTestVerifier(t, Config{
		StatePath:    path,
		Registration: license.DeviceRegistration{Platform: "macos", Architecture: "arm64", AppVersion: "1.0.0"},
		Halt:         func() { atomic.AddInt32(&halt, 1) },
		Attest: func(baseURL, regJSON, bh string, cs *string, nowMs int64) (string, error) {
			return genuineAttestResult(), nil
		},
	})
	v.AttestOnce(t.Context())

	s := stateFromFile(t, path)
	if s.Grant == nil || !s.Grant.HasLifetimeUnlock {
		t.Fatalf("grant not persisted: %+v", s.Grant)
	}
	if s.LegacyUnsignedOK {
		t.Fatal("legacy flag should be cleared after a genuine attest")
	}
	if s.BanMarker != nil {
		t.Fatal("ban marker should be cleared on genuine")
	}
	if s.DeviceID != "device-1" {
		t.Fatalf("device id = %q, want device-1", s.DeviceID)
	}
	if atomic.LoadInt32(&halt) != 0 {
		t.Fatal("halt must not fire on genuine")
	}
}

func TestAttestOnceBannedWritesMarkerAndHalts(t *testing.T) {
	path := mustTempState(t, license.LicenseState{
		InstallID: "install-1",
		Snapshot:  license.Snapshot{Transactions: []license.Transaction{{ProductID: license.LifetimeUnlockProductID, PurchaseDate: license.UTCDate(2026, 6, 3)}}},
	})
	var halt int32
	v := newTestVerifier(t, Config{
		StatePath:    path,
		Registration: license.DeviceRegistration{Platform: "macos"},
		Halt:         func() { atomic.AddInt32(&halt, 1) },
		Attest: func(baseURL, regJSON, bh string, cs *string, nowMs int64) (string, error) {
			return bannedAttestResult(), nil
		},
	})
	v.AttestOnce(t.Context())

	s := stateFromFile(t, path)
	if s.BanMarker == nil || s.BanMarker.BanID != "clhb_1" {
		t.Fatalf("ban marker not persisted: %+v", s.BanMarker)
	}
	if s.Grant != nil {
		t.Fatal("grant should be cleared on ban")
	}
	if atomic.LoadInt32(&halt) != 1 {
		t.Fatalf("halt should fire once, got %d", halt)
	}

	// A second attest while the ban is active is a no-op (no re-attest, no
	// second halt) — re-arm Attest to detect calls.
	var calls int32
	v2 := newTestVerifier(t, Config{
		StatePath:    path,
		Registration: license.DeviceRegistration{Platform: "macos"},
		Halt:         func() { atomic.AddInt32(&halt, 1) },
		Attest: func(baseURL, regJSON, bh string, cs *string, nowMs int64) (string, error) {
			atomic.AddInt32(&calls, 1)
			return bannedAttestResult(), nil
		},
	})
	v2.AttestOnce(t.Context())
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("should not re-attest while already banned; calls=%d", calls)
	}
	if atomic.LoadInt32(&halt) != 1 {
		t.Fatalf("halt should not fire again; got %d", halt)
	}
}

func TestAttestOnceNetworkErrorRecordsFailure(t *testing.T) {
	path := mustTempState(t, license.LicenseState{
		InstallID: "install-1",
		Snapshot: license.Snapshot{
			Transactions:   []license.Transaction{{ProductID: license.LifetimeUnlockProductID, PurchaseDate: license.UTCDate(2026, 6, 3)}},
			LastVerifiedAt: ptr(license.UTCDate(2026, 6, 10)),
		},
	})
	v := newTestVerifier(t, Config{
		StatePath:    path,
		Registration: license.DeviceRegistration{Platform: "macos"},
		Halt:         func() {},
		Attest: func(baseURL, regJSON, bh string, cs *string, nowMs int64) (string, error) {
			return "", errNetwork
		},
	})
	v.AttestOnce(t.Context())

	s := stateFromFile(t, path)
	if s.Snapshot.LastVerificationFailedAt == nil {
		t.Fatal("network error should record a verification failure")
	}
}

func TestAttestOnceTrialOnlyIsNoOp(t *testing.T) {
	path := mustTempState(t, license.LicenseState{Snapshot: license.Snapshot{TrialStartDate: ptr(license.UTCDate(2026, 6, 3))}})
	var calls int32
	v := newTestVerifier(t, Config{
		StatePath:    path,
		Registration: license.DeviceRegistration{Platform: "macos"},
		Halt:         func() {},
		Attest: func(baseURL, regJSON, bh string, cs *string, nowMs int64) (string, error) {
			atomic.AddInt32(&calls, 1)
			return genuineAttestResult(), nil
		},
	})
	v.AttestOnce(t.Context())
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("trial-only device should not attest; calls=%d", calls)
	}
}

func TestReadStateToleratesBareSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "license.json")
	snap := license.Snapshot{TrialStartDate: ptr(license.UTCDate(2026, 6, 3))}
	b, _ := json.Marshal(snap)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	v := newTestVerifier(t, Config{StatePath: path, Registration: license.DeviceRegistration{Platform: "macos"}})
	state, legacy, err := v.readState()
	if err != nil {
		t.Fatal(err)
	}
	if !legacy || !state.LegacyUnsignedOK {
		t.Fatalf("bare snapshot should be wrapped as legacy: legacy=%v state=%+v", legacy, state)
	}
	if state.Snapshot.TrialStartDate == nil {
		t.Fatal("trial start should be preserved")
	}
}

// errNetwork is a sentinel for the network-error seam.
type netErr string

func (e netErr) Error() string { return string(e) }

var errNetwork netErr = "network unavailable"
