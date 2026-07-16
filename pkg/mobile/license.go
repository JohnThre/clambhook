package mobile

import "github.com/JohnThre/clambhook/internal/licensebridge"

// This file exposes the shared license domain to mobile clients through
// gomobile. The implementation lives in internal/licensebridge (pure Go, no
// cgo) so the same bridge serves both gomobile (Android) and the Linux/TUI
// license helper (cmd/clambhook-license). Kotlin owns persistence (encrypted
// store + DataStore) and passes the cached snapshot JSON in; the bridge performs
// evaluation, the store.swiphtgroup.com HTTP calls, and the "apply server
// response" transform, returning JSON for Kotlin to persist and render.

// NewLicenseInstallID returns a fresh lowercase install identifier. Callers
// persist it once and reuse it for the lifetime of the install.
func NewLicenseInstallID() string { return licensebridge.NewLicenseInstallID() }

// LicenseValidationBaseURL is the license backend base URL.
func LicenseValidationBaseURL() string { return licensebridge.LicenseValidationBaseURL() }

// LicensePortalURL is the device-seat management portal URL.
func LicensePortalURL() string { return licensebridge.LicensePortalURL() }

// LicenseCommercialTermsJSON returns the commercial contract constants.
func LicenseCommercialTermsJSON() (string, error) {
	return licensebridge.LicenseCommercialTermsJSON()
}

// EnsureLicenseTrialJSON seeds the trial start date when absent and returns the
// snapshot to persist. Call once on first launch.
func EnsureLicenseTrialJSON(snapshotJSON string, nowUnixMillis int64) (string, error) {
	return licensebridge.EnsureLicenseTrialJSON(snapshotJSON, nowUnixMillis)
}

// EvaluateLicenseJSON evaluates the snapshot and returns the decision JSON.
func EvaluateLicenseJSON(snapshotJSON string, nowUnixMillis int64) (string, error) {
	return licensebridge.EvaluateLicenseJSON(snapshotJSON, nowUnixMillis)
}

// LicenseStatusJSON returns everything the UI needs to render license state:
// the decision, the status rows, and any active recovery banners.
// updatePublishedAtMillis is the pending update's publish time (0 when none).
func LicenseStatusJSON(snapshotJSON string, updatePublishedAtMillis, nowUnixMillis int64) (string, error) {
	return licensebridge.LicenseStatusJSON(snapshotJSON, updatePublishedAtMillis, nowUnixMillis)
}

// LicenseUpdateAllowed reports whether a release may be installed under the
// cached license. publishedAtMillis is 0 when the release date is unknown.
func LicenseUpdateAllowed(snapshotJSON string, publishedAtMillis, nowUnixMillis int64) (bool, error) {
	return licensebridge.LicenseUpdateAllowed(snapshotJSON, publishedAtMillis, nowUnixMillis)
}

// ActivateLicenseJSON activates or refreshes this device against the backend
// and returns the applied license payload (grant, snapshot, device state,
// decision) for Kotlin to persist. On any error the caller records a
// verification failure via MarkLicenseVerificationFailureJSON.
func ActivateLicenseJSON(baseURL, licenseKey, email, deviceRegJSON string, nowUnixMillis int64) (string, error) {
	return licensebridge.ActivateLicenseJSON(baseURL, licenseKey, email, deviceRegJSON, nowUnixMillis)
}

// LicenseDeviceActionJSON runs a device-seat action (deactivate, reactivate, or
// transfer) and returns the applied license payload.
func LicenseDeviceActionJSON(baseURL, action, licenseKey, installID, deviceID, deviceRegJSON string, nowUnixMillis int64) (string, error) {
	return licensebridge.LicenseDeviceActionJSON(baseURL, action, licenseKey, installID, deviceID, deviceRegJSON, nowUnixMillis)
}

// MarkLicenseVerificationFailureJSON records a failed verification against the
// snapshot (starting or extending offline grace) and returns the updated
// snapshot plus the re-evaluated decision.
func MarkLicenseVerificationFailureJSON(snapshotJSON string, nowUnixMillis int64) (string, error) {
	return licensebridge.MarkLicenseVerificationFailureJSON(snapshotJSON, nowUnixMillis)
}
