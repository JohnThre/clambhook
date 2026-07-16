package license

import (
	"fmt"
	"time"
)

// ProductStateKind identifies a row in the license status list.
type ProductStateKind string

const (
	ProductStateTrial             ProductStateKind = "trial"
	ProductStateLifetimeUnlocked  ProductStateKind = "lifetimeUnlocked"
	ProductStatePaidUpdateWindow  ProductStateKind = "paidUpdateWindow"
	ProductStateNewFeaturesLocked ProductStateKind = "newFeaturesLocked"
)

// ProductState is a single displayable license status row.
type ProductState struct {
	Kind     ProductStateKind `json:"kind"`
	Title    string           `json:"title"`
	Detail   string           `json:"detail"`
	IsActive bool             `json:"isActive"`
}

// ProductStates builds the license status rows for a decision, mirroring the
// Apple MobileLicenseProductStateBuilder copy.
func ProductStates(d Decision, features []Feature) []ProductState {
	if features == nil {
		features = DefaultFeatures()
	}
	states := make([]ProductState, 0, 4)

	if d.TrialEndsAt != nil {
		detail := fmt.Sprintf("Trial ended %s.", formatDate(*d.TrialEndsAt))
		if d.IsTrialActive() {
			detail = fmt.Sprintf("Trial ends %s.", formatDate(*d.TrialEndsAt))
		}
		states = append(states, ProductState{
			Kind:     ProductStateTrial,
			Title:    "One-calendar-month trial",
			Detail:   detail,
			IsActive: d.IsTrialActive(),
		})
	} else {
		states = append(states, ProductState{
			Kind:     ProductStateTrial,
			Title:    "One-calendar-month trial",
			Detail:   "Trial starts the first time this app records an access date.",
			IsActive: false,
		})
	}

	licenseDetail := "Buy or activate a ClambHook license to keep using ClambHook after free access."
	if d.HasLifetimeUnlock {
		licenseDetail = "Versions released during the included update window remain usable."
	}
	states = append(states, ProductState{
		Kind:     ProductStateLifetimeUnlocked,
		Title:    "ClambHook license",
		Detail:   licenseDetail,
		IsActive: d.HasLifetimeUnlock,
	})

	if d.UpdateCutoffDate != nil {
		states = append(states, ProductState{
			Kind:     ProductStatePaidUpdateWindow,
			Title:    fmt.Sprintf("Included updates through %s", formatDate(*d.UpdateCutoffDate)),
			Detail:   "All updates released on or before this date are included, and those app versions remain usable.",
			IsActive: d.HasLifetimeUnlock,
		})
	} else {
		states = append(states, ProductState{
			Kind:     ProductStatePaidUpdateWindow,
			Title:    "Included updates through DATE",
			Detail:   "A ClambHook license includes one year of all updates from the purchase date.",
			IsActive: false,
		})
	}

	var locked []Feature
	if d.UpdateCutoffDate != nil {
		for _, f := range features {
			if f.ReleaseDate.After(*d.UpdateCutoffDate) {
				locked = append(locked, f)
			}
		}
	}
	lockedDetail := "All updates released after the cutoff, including critical, bug, and security updates, require a USD 9.99 update-year renewal."
	if len(locked) > 0 {
		names := ""
		for i, f := range locked {
			if i > 0 {
				names += ", "
			}
			names += f.DisplayName
		}
		lockedDetail = fmt.Sprintf("Updates requiring renewal include: %s.", names)
	}
	states = append(states, ProductState{
		Kind:     ProductStateNewFeaturesLocked,
		Title:    "Later updates require renewal",
		Detail:   lockedDetail,
		IsActive: len(locked) > 0,
	})

	return states
}

// RecoveryKind identifies a license-related recovery banner.
type RecoveryKind string

const (
	RecoveryExpiredTrial          RecoveryKind = "expired_trial"
	RecoveryLicenseExpiredUpdates RecoveryKind = "license_expired_for_updates"
)

// RecoverySeverity classifies banner urgency.
type RecoverySeverity string

const (
	SeverityInfo    RecoverySeverity = "info"
	SeverityWarning RecoverySeverity = "warning"
	SeverityError   RecoverySeverity = "error"
)

// RecoveryAction is a suggested user action on a recovery banner.
type RecoveryAction string

const (
	ActionBuyLicense        RecoveryAction = "buy_license"
	ActionActivateLicense   RecoveryAction = "activate_license"
	ActionOpenLicensePortal RecoveryAction = "open_license_portal"
	ActionRenewUpdates      RecoveryAction = "renew_updates"
	ActionSupport           RecoveryAction = "support"
)

// RecoveryState is a displayable license recovery banner.
type RecoveryState struct {
	Kind             RecoveryKind     `json:"kind"`
	Severity         RecoverySeverity `json:"severity"`
	Title            string           `json:"title"`
	Message          string           `json:"message"`
	PrimaryAction    RecoveryAction   `json:"primaryAction"`
	SecondaryActions []RecoveryAction `json:"secondaryActions"`
	DiagnosticText   string           `json:"diagnosticText"`
}

// ExpiredTrialState returns the trial-ended banner when access is locked, or
// nil when the app is still usable.
func ExpiredTrialState(d Decision) *RecoveryState {
	if d.CanUseApp() {
		return nil
	}
	message := "Buy or activate a ClambHook license to continue."
	if d.TrialEndsAt != nil {
		message = fmt.Sprintf(
			"The one-calendar-month trial ended %s. Buy or activate a USD 99.99 one-time ClambHook license to continue.",
			formatDate(*d.TrialEndsAt),
		)
	}
	return &RecoveryState{
		Kind:             RecoveryExpiredTrial,
		Severity:         SeverityWarning,
		Title:            "Trial ended",
		Message:          message,
		PrimaryAction:    ActionBuyLicense,
		SecondaryActions: []RecoveryAction{ActionActivateLicense, ActionOpenLicensePortal, ActionSupport},
	}
}

// LicenseExpiredForUpdatesState returns the update-window-ended banner when a
// licensed device faces a release published after its cutoff, else nil.
func LicenseExpiredForUpdatesState(d Decision, manifestPublishedAt *time.Time, now time.Time) *RecoveryState {
	if !d.HasLifetimeUnlock || d.UpdateCutoffDate == nil {
		return nil
	}
	releaseDate := now
	if manifestPublishedAt != nil {
		releaseDate = *manifestPublishedAt
	}
	if !releaseDate.After(*d.UpdateCutoffDate) {
		return nil
	}
	cutoff := formatDate(*d.UpdateCutoffDate)
	var message string
	if manifestPublishedAt != nil {
		message = fmt.Sprintf(
			"This update was published %s, after your included update window ended %s. Your installed version keeps working; renew updates for USD 9.99 to install releases after the cutoff, including critical, bug, and security updates.",
			formatDate(*manifestPublishedAt), cutoff,
		)
	} else {
		message = fmt.Sprintf(
			"Your included update window ended %s. Your installed version keeps working; renew updates for USD 9.99 to install releases after the cutoff, including critical, bug, and security updates.",
			cutoff,
		)
	}
	return &RecoveryState{
		Kind:             RecoveryLicenseExpiredUpdates,
		Severity:         SeverityWarning,
		Title:            "Update window ended",
		Message:          message,
		PrimaryAction:    ActionRenewUpdates,
		SecondaryActions: []RecoveryAction{ActionOpenLicensePortal, ActionActivateLicense, ActionSupport},
		DiagnosticText:   PaidUpdatePolicyCopy(*d.UpdateCutoffDate),
	}
}
