// Package licensebridge exposes the shared license domain (internal/license)
// as a set of pure-Go, JSON-in/JSON-out functions with no cgo, engine, or
// protocol dependencies. It is the single source of truth for every non-Apple
// client bridge: the gomobile surface (pkg/mobile) and the Linux/TUI license
// helper (cmd/clambhook-license) both delegate here so evaluation, date math,
// the "apply server response" transform, and the store.swiphtgroup.com HTTP
// calls live in exactly one place.
//
// Callers own persistence (encrypted key store + cached snapshot JSON) and pass
// the cached snapshot JSON in; the bridge returns JSON to persist and render.
package licensebridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/JohnThre/clambhook/internal/license"
)

var licenseHTTPClient = &http.Client{Timeout: 20 * time.Second}

func marshalString(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// nowOrDefault converts a Unix-millis argument to a UTC time. A non-positive
// value means "use the current time".
func nowOrDefault(unixMillis int64) time.Time {
	if unixMillis <= 0 {
		return time.Now().UTC()
	}
	return time.UnixMilli(unixMillis).UTC()
}

// optionalMillis converts a Unix-millis argument to an optional UTC time.
// A non-positive value means "unknown/nil".
func optionalMillis(unixMillis int64) *time.Time {
	if unixMillis <= 0 {
		return nil
	}
	t := time.UnixMilli(unixMillis).UTC()
	return &t
}

func decodeSnapshot(snapshotJSON string) (license.Snapshot, error) {
	var snap license.Snapshot
	trimmed := strings.TrimSpace(snapshotJSON)
	if trimmed == "" {
		return snap, nil
	}
	if err := json.Unmarshal([]byte(trimmed), &snap); err != nil {
		return snap, fmt.Errorf("license: decode snapshot: %w", err)
	}
	return snap, nil
}

// NewLicenseInstallID returns a fresh lowercase install identifier. Callers
// persist it once and reuse it for the lifetime of the install.
func NewLicenseInstallID() string {
	return strings.ToLower(uuid.NewString())
}

// LicenseValidationBaseURL is the license backend base URL.
func LicenseValidationBaseURL() string { return license.ValidationBaseURL }

// LicensePortalURL is the device-seat management portal URL.
func LicensePortalURL() string { return license.PortalURL }

// LicenseCommercialTermsJSON returns the commercial contract constants.
func LicenseCommercialTermsJSON() (string, error) {
	return marshalString(map[string]any{
		"licensePriceUSD":     license.LicensePriceUSD,
		"paidUpdatePriceUSD":  license.PaidUpdatePriceUSD,
		"includedUpdateYears": license.IncludedUpdateYears,
		"maxActiveDevices":    license.MaxActiveDevices,
		"trialMonths":         license.TrialMonths,
	})
}

// EnsureLicenseTrialJSON seeds the trial start date when absent and returns the
// snapshot to persist. Call once on first launch.
func EnsureLicenseTrialJSON(snapshotJSON string, nowUnixMillis int64) (string, error) {
	snap, err := decodeSnapshot(snapshotJSON)
	if err != nil {
		return "", err
	}
	return marshalString(license.EnsureTrialStart(snap, nowOrDefault(nowUnixMillis)))
}

// EvaluateLicenseJSON evaluates the snapshot and returns the decision JSON.
func EvaluateLicenseJSON(snapshotJSON string, nowUnixMillis int64) (string, error) {
	snap, err := decodeSnapshot(snapshotJSON)
	if err != nil {
		return "", err
	}
	return marshalString(license.Evaluate(snap, nil, nowOrDefault(nowUnixMillis)))
}

type licenseStatusPayload struct {
	Decision                 license.Decision       `json:"decision"`
	ProductStates            []license.ProductState `json:"productStates"`
	ExpiredTrial             *license.RecoveryState `json:"expiredTrial,omitempty"`
	LicenseExpiredForUpdates *license.RecoveryState `json:"licenseExpiredForUpdates,omitempty"`
}

// LicenseStatusJSON returns everything the UI needs to render license state:
// the decision, the status rows, and any active recovery banners.
// updatePublishedAtMillis is the pending update's publish time (0 when none).
func LicenseStatusJSON(snapshotJSON string, updatePublishedAtMillis, nowUnixMillis int64) (string, error) {
	snap, err := decodeSnapshot(snapshotJSON)
	if err != nil {
		return "", err
	}
	now := nowOrDefault(nowUnixMillis)
	decision := license.Evaluate(snap, nil, now)
	payload := licenseStatusPayload{
		Decision:      decision,
		ProductStates: license.ProductStates(decision, nil),
		ExpiredTrial:  license.ExpiredTrialState(decision),
	}
	if updatePublishedAtMillis > 0 {
		payload.LicenseExpiredForUpdates = license.LicenseExpiredForUpdatesState(decision, optionalMillis(updatePublishedAtMillis), now)
	} else {
		payload.LicenseExpiredForUpdates = license.LicenseExpiredForUpdatesState(decision, nil, now)
	}
	return marshalString(payload)
}

// LicenseUpdateAllowed reports whether a release may be installed under the
// cached license. publishedAtMillis is 0 when the release date is unknown.
func LicenseUpdateAllowed(snapshotJSON string, publishedAtMillis, nowUnixMillis int64) (bool, error) {
	snap, err := decodeSnapshot(snapshotJSON)
	if err != nil {
		return false, err
	}
	now := nowOrDefault(nowUnixMillis)
	decision := license.Evaluate(snap, nil, now)
	return license.CanInstallUpdate(decision, optionalMillis(publishedAtMillis), now), nil
}

type appliedLicensePayload struct {
	Grant       json.RawMessage     `json:"grant"`
	Snapshot    license.Snapshot    `json:"snapshot"`
	DeviceState license.DeviceState `json:"deviceState"`
	Decision    license.Decision    `json:"decision"`
}

// applyServerResponse mirrors the macOS MacLicenseManager.apply: persist the
// grant, build a local snapshot, normalize device state, and evaluate.
func applyServerResponse(raw []byte, installID string, now time.Time) (string, error) {
	var resp license.ServerResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("license: decode server response: %w", err)
	}
	// Verify the grant signature when the server signs (key_id present). A
	// legacy/unsigned grant (key_id empty) is accepted unchanged. A bad
	// signature is a verification failure: callers record it via
	// MarkLicenseVerificationFailureJSON so the device falls into offline grace
	// rather than trusting a forged grant.
	if err := license.VerifyGrant(resp.Grant); err != nil {
		return "", fmt.Errorf("license: %w", err)
	}
	grantJSON, err := json.Marshal(resp.Grant)
	if err != nil {
		return "", err
	}
	snap := resp.Snapshot.LicenseSnapshot(now)
	deviceState := resp.DeviceState.Normalized().WithCurrentInstallID(installID)
	decision := license.Evaluate(snap, nil, now)
	return marshalString(appliedLicensePayload{
		Grant:       grantJSON,
		Snapshot:    snap,
		DeviceState: deviceState,
		Decision:    decision,
	})
}

func postLicense(baseURL, path string, body any) ([]byte, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = license.ValidationBaseURL
	}
	url := fmt.Sprintf("%s/v1/devices/%s", base, path)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := licenseHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.Error != "" {
			return nil, fmt.Errorf("%s", envelope.Error)
		}
		return nil, fmt.Errorf("license request failed (%d)", resp.StatusCode)
	}
	return data, nil
}

func decodeRegistration(deviceRegJSON string) (license.DeviceRegistration, error) {
	var reg license.DeviceRegistration
	if err := json.Unmarshal([]byte(deviceRegJSON), &reg); err != nil {
		return reg, fmt.Errorf("license: decode device registration: %w", err)
	}
	return reg, nil
}

// ActivateLicenseJSON activates or refreshes this device against the backend
// and returns the applied license payload (grant, snapshot, device state,
// decision) for the caller to persist. On any error the caller records a
// verification failure via MarkLicenseVerificationFailureJSON.
func ActivateLicenseJSON(baseURL, licenseKey, email, deviceRegJSON string, nowUnixMillis int64) (string, error) {
	reg, err := decodeRegistration(deviceRegJSON)
	if err != nil {
		return "", err
	}
	raw, err := postLicense(baseURL, "activate", license.ActivationRequest{
		LicenseKey: strings.TrimSpace(licenseKey),
		Email:      strings.TrimSpace(email),
		Device:     reg,
	})
	if err != nil {
		return "", err
	}
	return applyServerResponse(raw, reg.InstallID, nowOrDefault(nowUnixMillis))
}

// LicenseDeviceActionJSON runs a device-seat action (deactivate, reactivate, or
// transfer) and returns the applied license payload.
func LicenseDeviceActionJSON(baseURL, action, licenseKey, installID, deviceID, deviceRegJSON string, nowUnixMillis int64) (string, error) {
	switch action {
	case "deactivate", "reactivate", "transfer":
	default:
		return "", fmt.Errorf("license: unsupported device action %q", action)
	}
	reg, err := decodeRegistration(deviceRegJSON)
	if err != nil {
		return "", err
	}
	raw, err := postLicense(baseURL, action, license.DeviceActionRequest{
		LicenseKey: strings.TrimSpace(licenseKey),
		InstallID:  installID,
		DeviceID:   deviceID,
		Device:     reg,
	})
	if err != nil {
		return "", err
	}
	return applyServerResponse(raw, installID, nowOrDefault(nowUnixMillis))
}

type verificationFailurePayload struct {
	Snapshot license.Snapshot `json:"snapshot"`
	Decision license.Decision `json:"decision"`
}

// MarkLicenseVerificationFailureJSON records a failed verification against the
// snapshot. The first failure after a success starts offline grace; subsequent
// consecutive failures preserve that timestamp so they cannot extend grace.
func MarkLicenseVerificationFailureJSON(snapshotJSON string, nowUnixMillis int64) (string, error) {
	snap, err := decodeSnapshot(snapshotJSON)
	if err != nil {
		return "", err
	}
	now := nowOrDefault(nowUnixMillis)
	if snap.LastVerificationFailedAt == nil ||
		(snap.LastVerifiedAt != nil && snap.LastVerificationFailedAt.Before(*snap.LastVerifiedAt)) {
		snap.LastVerificationFailedAt = &now
	}
	snap.CachedAt = now
	return marshalString(verificationFailurePayload{
		Snapshot: snap,
		Decision: license.Evaluate(snap, nil, now),
	})
}

// BuildSecret is the build-embedded secret HMAC'd into the attestation token to
// prove the binary carries it. It is injected at build time (e.g. via -ldflags
// -X) and is NOT committed; an empty secret yields an empty token, which the
// server rejects, so dev/CI attests fail closed into offline grace.
var BuildSecret string

// attestResult is the unified outcome of an attestation call, returned as JSON
// by AttestLicenseJSON. The daemon persists a LicenseState from it: on genuine
// it stores the signed Grant + Snapshot + device identifiers; on banned it
// stores the BanMarker and halts.
type attestResult struct {
	Genuine     bool                 `json:"genuine"`
	Banned      bool                 `json:"banned"`
	Grant       *license.ServerGrant `json:"grant,omitempty"`
	Snapshot    license.Snapshot     `json:"snapshot,omitempty"`
	DeviceState license.DeviceState  `json:"device_state,omitempty"`
	Decision    license.Decision     `json:"decision"`
	BanMarker   *license.BanMarker   `json:"ban_marker,omitempty"`
}

type attestRequestBody struct {
	InstallID   string                     `json:"install_id"`
	Device      license.DeviceRegistration `json:"device,omitempty"`
	Attestation map[string]any             `json:"attestation"`
}

type attestBannedBody struct {
	Banned           bool            `json:"banned"`
	BanReason        string          `json:"ban_reason"`
	SupportEmail     string          `json:"support_email"`
	DisputeURL       string          `json:"dispute_url"`
	DisputeThreadURL *string         `json:"dispute_thread_url"`
	Ban              json.RawMessage `json:"ban"`
}

// postAttest POSTs the attestation and returns the raw body + status (it does
// not error on non-2xx; the caller distinguishes genuine 200 from banned 403).
func postAttest(baseURL, path string, body any) ([]byte, int, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = license.ValidationBaseURL
	}
	url := fmt.Sprintf("%s/v1/devices/%s", base, path)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := licenseHTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

// BuildAttestationJSON builds the `attestation` object the client sends to the
// /attest endpoint: a key_id, the HMAC build_token proving the build-embedded
// secret, the binary hash / code-sign identity, a nonce, and a timestamp. The
// daemon's SelfAttestation supplies the binary hash / code-sign identity.
func BuildAttestationJSON(installID, binaryHash string, codeSignIdentity *string, nonce string, nowUnixMillis int64) (string, error) {
	ts := nowOrDefault(nowUnixMillis).UnixMilli()
	token := license.BuildAttestationToken(license.CLAMBHOOKGrantKeyID, installID, binaryHash, codeSignIdentity, nonce, ts, BuildSecret)
	return marshalString(map[string]any{
		"key_id":             license.CLAMBHOOKGrantKeyID,
		"build_token":        token,
		"binary_hash":        binaryHash,
		"code_sign_identity": codeSignIdentity,
		"nonce":              nonce,
		"timestamp":          ts,
	})
}

// AttestLicenseJSON performs a genuine-system attestation against the backend.
// On a genuine 200 it verifies the returned grant signature and returns the
// applied license payload (grant + snapshot + device state + decision). On a
// banned 403 it verifies the signed ban verdict and returns a ban marker. On a
// network/server error it returns an error so the caller records a
// verification failure (offline grace) via MarkLicenseVerificationFailureJSON.
func AttestLicenseJSON(baseURL, deviceRegJSON, binaryHash string, codeSignIdentity *string, nowUnixMillis int64) (string, error) {
	reg, err := decodeRegistration(deviceRegJSON)
	if err != nil {
		return "", err
	}
	now := nowOrDefault(nowUnixMillis)
	nonce := strings.ToLower(uuid.NewString())
	attJSON, err := BuildAttestationJSON(reg.InstallID, binaryHash, codeSignIdentity, nonce, nowUnixMillis)
	if err != nil {
		return "", err
	}
	var att map[string]any
	if err := json.Unmarshal([]byte(attJSON), &att); err != nil {
		return "", err
	}
	body := attestRequestBody{
		InstallID:   reg.InstallID,
		Device:      reg,
		Attestation: att,
	}
	raw, status, err := postAttest(baseURL, "attest", body)
	if err != nil {
		return "", err
	}
	if status == http.StatusOK {
		applied, err := applyServerResponse(raw, reg.InstallID, now)
		if err != nil {
			return "", err
		}
		var appliedPayload appliedLicensePayload
		if err := json.Unmarshal([]byte(applied), &appliedPayload); err != nil {
			return "", err
		}
		var grant license.ServerGrant
		if len(appliedPayload.Grant) > 0 {
			if err := json.Unmarshal(appliedPayload.Grant, &grant); err != nil {
				return "", fmt.Errorf("license: decode grant: %w", err)
			}
		}
		return marshalString(attestResult{
			Genuine:     true,
			Grant:       &grant,
			Snapshot:    appliedPayload.Snapshot,
			DeviceState: appliedPayload.DeviceState,
			Decision:    appliedPayload.Decision,
		})
	}
	if status == http.StatusForbidden {
		var banned attestBannedBody
		if err := json.Unmarshal(raw, &banned); err != nil || !banned.Banned || len(banned.Ban) == 0 {
			return "", fmt.Errorf("license: attest rejected (%d)", status)
		}
		var m license.BanMarker
		if err := json.Unmarshal(banned.Ban, &m); err != nil {
			return "", fmt.Errorf("license: decode ban verdict: %w", err)
		}
		m.SupportEmail = banned.SupportEmail
		m.DisputeURL = banned.DisputeURL
		m.DisputeThreadURL = banned.DisputeThreadURL
		if err := license.VerifyBanMarker(m, reg.InstallID); err != nil {
			return "", fmt.Errorf("license: %w", err)
		}
		decision := license.EvaluateState(license.LicenseState{InstallID: reg.InstallID, BanMarker: &m}, nil, now)
		return marshalString(attestResult{
			Banned:    true,
			BanMarker: &m,
			Decision:  decision,
		})
	}
	return "", fmt.Errorf("license: attest failed (%d)", status)
}
