// Package licenseverify runs the daemon-side genuine-system verification loop:
// on startup, on each network change reported by netwatch, and on a 6-hour
// timer while online, the verifier attests this device against
// store.swiphtgroup.com. A genuine result refreshes the locally persisted
// signed grant; a banned result writes a durable ban marker and hard-stops
// routing (the halt hook); a network error records a verification failure so
// the existing offline-grace path applies.
//
// The daemon is the trust root for enforcement (it gates routing for every
// platform and runs without a UI open), so the loop lives here. The Apple,
// Linux, Android, and TUI clients keep their own activation UIs but read the
// same persisted LicenseState envelope the daemon enforces.
package licenseverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/license"
	"github.com/JohnThre/clambhook/internal/licensebridge"
)

// defaultInterval is the attest cadence while online, in addition to
// startup and network-change triggers.
const defaultInterval = 6 * time.Hour

// debounce coalesces a burst of network-change signals so a flapping
// interface does not hammer the endpoint.
const debounce = 15 * time.Second

// Config wires the verifier to the daemon. The *Func fields are test seams;
// nil falls back to the production defaults (licensebridge + file IO + real
// clock).
type Config struct {
	// StatePath is the persisted LicenseState envelope path (the daemon's
	// -license flag). A bare Snapshot file is tolerated and wrapped as a
	// legacy LicenseState on read.
	StatePath string

	// Registration describes this device to the backend (platform/arch/
	// version/display name). InstallID is taken from the persisted state, not
	// the registration, so the same registration is reused across installs.
	Registration license.DeviceRegistration

	// SelfAttestation returns the binary hash and (on macOS) the code-signing
	// identity the backend checks against the genuine-build allowlist. nil →
	// SelfAttestation().
	SelfAttestation func() (binaryHash string, codeSignIdentity *string)

	// Halt is called once when a ban is detected. It should hard-stop routing
	// (tear down active connections and stop listeners). Must be idempotent.
	Halt func()

	// Interval overrides the 6h default (tests).
	Interval time.Duration

	// NetwatchCh, when non-nil, delivers a signal whenever the active network
	// changes; the verifier debounces and re-attests. The daemon adapts the
	// netwatch.Watcher channel to this.
	NetwatchCh <-chan struct{}

	// Test seams.
	Now         func() time.Time
	Attest      func(baseURL, deviceRegJSON, binaryHash string, codeSign *string, nowMs int64) (string, error)
	MarkFailure func(snapshotJSON string, nowMs int64) (string, error)
	Logf        func(format string, args ...any)
}

// Verifier runs the genuine-system verification loop.
type Verifier struct {
	cfg        Config
	selfAttest func() (string, *string)
	haltOnce   sync.Once
	stopped    chan struct{}
	mu         sync.Mutex // guards on-demand AttestOnce against the loop
}

// New constructs a Verifier. It does not start it.
func New(cfg Config) *Verifier {
	if cfg.SelfAttestation == nil {
		cfg.SelfAttestation = SelfAttestation
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Attest == nil {
		cfg.Attest = licensebridge.AttestLicenseJSON
	}
	if cfg.MarkFailure == nil {
		cfg.MarkFailure = licensebridge.MarkLicenseVerificationFailureJSON
	}
	if cfg.Logf == nil {
		cfg.Logf = func(format string, args ...any) { log.Printf("licenseverify: "+format, args...) }
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultInterval
	}
	return &Verifier{cfg: cfg, stopped: make(chan struct{}), selfAttest: cfg.SelfAttestation}
}

// Start launches the loop and returns a stop function (safe to call once).
func (v *Verifier) Start(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	go v.run(ctx)
	return func() {
		cancel()
		<-v.stopped
	}
}

func (v *Verifier) run(ctx context.Context) {
	defer close(v.stopped)

	// Attest once on startup so a device that comes online between launches
	// verifies immediately (the user's "verification activates immediately on
	// the next startup while online").
	v.AttestOnce(ctx)

	ticker := time.NewTicker(v.cfg.Interval)
	defer ticker.Stop()

	var debounceTimer *time.Timer
	arm := func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
		debounceTimer = time.NewTimer(debounce)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			v.AttestOnce(ctx)
		case _, ok := <-v.cfg.NetwatchCh:
			if !ok {
				return
			}
			arm()
		case <-func() <-chan time.Time {
			if debounceTimer == nil {
				return nil
			}
			return debounceTimer.C
		}():
			v.AttestOnce(ctx)
		}
	}
}

// AttestOnce performs a single attestation and applies the result to the
// persisted state. It is safe for concurrent use (the loop and any on-demand
// caller serialize on v.mu). It is a no-op for trial-only devices (no
// install_id) and for already-banned devices.
func (v *Verifier) AttestOnce(ctx context.Context) {
	v.mu.Lock()
	defer v.mu.Unlock()

	state, legacy, err := v.readState()
	if err != nil {
		v.cfg.Logf("read state: %v", err)
		return
	}

	// Trial-only (no install_id): nothing to attest; the local trial governs.
	if state.InstallID == "" {
		return
	}
	// Already banned and the marker is still active: no need to re-attest; the
	// halt hook has already fired.
	if state.BanMarker != nil && state.BanMarker.IsActive(v.cfg.Now()) {
		return
	}

	reg := v.cfg.Registration
	reg.InstallID = state.InstallID
	regJSON, _ := json.Marshal(reg)

	binaryHash, codeSign := v.selfAttest()
	nowMs := v.cfg.Now().UnixMilli()

	out, err := v.cfg.Attest(license.ValidationBaseURL, string(regJSON), binaryHash, codeSign, nowMs)
	if err != nil {
		// Network/server error: record a verification failure so offline grace
		// applies (matches the existing activation-failure path).
		v.recordFailure(state, legacy)
		v.cfg.Logf("attest failed: %v", err)
		return
	}

	var res licensebridgeAttestResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		v.cfg.Logf("decode attest result: %v", err)
		return
	}

	if res.Banned && res.BanMarker != nil {
		state.BanMarker = res.BanMarker
		state.Grant = nil
		state.LegacyUnsignedOK = false
		if err := v.writeState(state, legacy); err != nil {
			v.cfg.Logf("write banned state: %v", err)
		}
		v.cfg.Logf("banned: reason=%s", res.BanMarker.Reason)
		if v.cfg.Halt != nil {
			v.haltOnce.Do(v.cfg.Halt)
		}
		return
	}

	if res.Genuine {
		state.Grant = res.Grant
		state.Snapshot = res.Snapshot
		if reg.InstallID != "" {
			state.InstallID = reg.InstallID
		}
		if res.DeviceState.CurrentDeviceID != "" {
			state.DeviceID = res.DeviceState.CurrentDeviceID
		}
		state.BanMarker = nil
		state.LegacyUnsignedOK = false
		if err := v.writeState(state, legacy); err != nil {
			v.cfg.Logf("write genuine state: %v", err)
		}
		return
	}

	v.recordFailure(state, legacy)
}

// recordFailure marks a verification failure on the persisted snapshot (the
// first failure after a success starts offline grace; later ones preserve it).
func (v *Verifier) recordFailure(state license.LicenseState, legacy bool) {
	snapJSON, err := json.Marshal(state.Snapshot)
	if err != nil {
		return
	}
	out, err := v.cfg.MarkFailure(string(snapJSON), v.cfg.Now().UnixMilli())
	if err != nil {
		v.cfg.Logf("mark failure: %v", err)
		return
	}
	var payload struct {
		Snapshot license.Snapshot `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return
	}
	state.Snapshot = payload.Snapshot
	_ = v.writeState(state, legacy)
}

// readState loads the LicenseState envelope, tolerating a bare Snapshot file
// (pre-genuine-verification upgrade) by wrapping it with LegacyUnsignedOK.
func (v *Verifier) readState() (state license.LicenseState, legacy bool, err error) {
	data, rerr := os.ReadFile(v.cfg.StatePath)
	if rerr != nil {
		if errors.Is(rerr, os.ErrNotExist) {
			return license.LicenseState{}, false, nil
		}
		return license.LicenseState{}, false, rerr
	}
	trimmed := string(data)
	if trimmed == "" {
		return license.LicenseState{}, false, nil
	}
	if err := json.Unmarshal(data, &state); err == nil && (state.Grant != nil || state.InstallID != "" || state.BanMarker != nil || state.LegacyUnsignedOK) {
		return state, false, nil
	}
	// Fall back to a bare Snapshot (legacy file written by an older UI).
	var snap license.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return license.LicenseState{}, false, fmt.Errorf("decode license state: %w", err)
	}
	return license.LicenseState{Snapshot: snap, LegacyUnsignedOK: true}, true, nil
}

// writeState persists the envelope. When legacy is true the file was a bare
// Snapshot and the first successful attest migrates it to the envelope by
// writing the full LicenseState.
func (v *Verifier) writeState(state license.LicenseState, legacy bool) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(v.cfg.StatePath, data, 0o600)
}

// licensebridgeAttestResult mirrors licensebridge.attestResult (unexported
// there) field-for-field so this package can decode the JSON the bridge
// returns without an import cycle exposing the type.
type licensebridgeAttestResult struct {
	Genuine     bool                 `json:"genuine"`
	Banned      bool                 `json:"banned"`
	Grant       *license.ServerGrant `json:"grant,omitempty"`
	Snapshot    license.Snapshot     `json:"snapshot,omitempty"`
	DeviceState license.DeviceState  `json:"device_state,omitempty"`
	Decision    license.Decision     `json:"decision"`
	BanMarker   *license.BanMarker   `json:"ban_marker,omitempty"`
}
