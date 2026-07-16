// Package license implements ClambHook's direct-sale license domain: trial,
// lifetime unlock, paid update-year windows, offline grace, and device-seat
// state. It is the shared source of truth for non-Apple clients (Android via
// pkg/mobile, and later Linux/TUI). The wire and persisted JSON shapes mirror
// the Apple ClambhookShared implementation so the same store.swiphtgroup.com
// backend serves every platform.
package license

import (
	"encoding/json"
	"strings"
	"time"
)

// Commercial contract constants. These MUST match the published terms and the
// Apple MobileLicenseCommercialTerms values.
const (
	LicensePriceUSD         = "99.99"
	PaidUpdatePriceUSD      = "9.99"
	IncludedUpdateYears     = 1
	MaxActiveDevices        = 10
	TrialMonths             = 1
	OfflineGraceDays        = 7
	LifetimeUnlockProductID = "org.jpfchang.clambhook.unlock.lifetime"
	FeatureUpdateProductID  = "org.jpfchang.clambhook.feature_update"
	ValidationBaseURL       = "https://store.swiphtgroup.com/clambhook/license"
	PortalURL               = "https://store.swiphtgroup.com/clambhook/portal"
)

// AccessReason is the resolved access state for a license snapshot.
type AccessReason string

const (
	ReasonTrial        AccessReason = "trial"
	ReasonLifetime     AccessReason = "lifetime"
	ReasonOfflineGrace AccessReason = "offlineGrace"
	ReasonLocked       AccessReason = "locked"
)

// ProductKind classifies a purchased transaction.
type ProductKind int

const (
	ProductUnknown ProductKind = iota
	ProductLifetimeUnlock
	ProductPaidUpdate
)

// ProductKindFor maps a product identifier to its kind. The renewal SKU
// tolerates dated variants ("…feature_update.YYYY") so older grants resolve.
func ProductKindFor(id string) ProductKind {
	switch {
	case id == LifetimeUnlockProductID:
		return ProductLifetimeUnlock
	case id == FeatureUpdateProductID || strings.HasPrefix(id, FeatureUpdateProductID+"."):
		return ProductPaidUpdate
	default:
		return ProductUnknown
	}
}

// Transaction is a single purchase (lifetime unlock or paid update year).
// JSON keys are camelCase to match the Apple snapshot/grant encoding.
type Transaction struct {
	ProductID      string     `json:"productID"`
	PurchaseDate   time.Time  `json:"purchaseDate"`
	RevocationDate *time.Time `json:"revocationDate,omitempty"`
}

// Kind reports the transaction's product kind.
func (t Transaction) Kind() ProductKind { return ProductKindFor(t.ProductID) }

// IsActive reports whether the transaction has not been revoked/refunded.
func (t Transaction) IsActive() bool { return t.RevocationDate == nil }

// Snapshot is the cached, persisted license state used for offline evaluation.
// JSON keys are camelCase to match the Apple MobileLicenseSnapshot encoding.
type Snapshot struct {
	TrialStartDate           *time.Time    `json:"trialStartDate"`
	Transactions             []Transaction `json:"transactions"`
	LastVerifiedAt           *time.Time    `json:"lastVerifiedAt"`
	LastVerificationFailedAt *time.Time    `json:"lastVerificationFailedAt"`
	CachedAt                 time.Time     `json:"cachedAt"`
}

// Feature identifiers gated by the update-year window.
type FeatureID string

const (
	FeatureTunnelRouting      FeatureID = "tunnel.routing"
	FeatureProfileManagement  FeatureID = "profile.management"
	FeatureRoutingRules       FeatureID = "routing.rules"
	FeatureActivityInspection FeatureID = "activity.inspection"
	FeatureHTTPMetadata       FeatureID = "http.metadata"
	FeatureWidgets            FeatureID = "widgets"
)

// Feature couples an identifier with the release date that gates it.
type Feature struct {
	ID          FeatureID `json:"id"`
	DisplayName string    `json:"displayName"`
	ReleaseDate time.Time `json:"releaseDate"`
}

// V1ReleaseDate is the release date of the initial feature set.
var V1ReleaseDate = UTCDate(2026, 6, 3)

// DefaultFeatures is the shipping feature catalog.
func DefaultFeatures() []Feature {
	return []Feature{
		{ID: FeatureTunnelRouting, DisplayName: "Tunnel Routing", ReleaseDate: V1ReleaseDate},
		{ID: FeatureProfileManagement, DisplayName: "Profile Management", ReleaseDate: V1ReleaseDate},
		{ID: FeatureRoutingRules, DisplayName: "Routing Rules", ReleaseDate: V1ReleaseDate},
		{ID: FeatureActivityInspection, DisplayName: "Activity Inspection", ReleaseDate: V1ReleaseDate},
		{ID: FeatureHTTPMetadata, DisplayName: "HTTP Metadata", ReleaseDate: V1ReleaseDate},
		{ID: FeatureWidgets, DisplayName: "Widgets", ReleaseDate: V1ReleaseDate},
	}
}

// Decision is the evaluated access outcome for a snapshot at a point in time.
type Decision struct {
	Reason             AccessReason `json:"reason"`
	TrialStartDate     *time.Time   `json:"trialStartDate"`
	TrialEndsAt        *time.Time   `json:"trialEndsAt"`
	TrialDaysRemaining int          `json:"trialDaysRemaining"`
	HasLifetimeUnlock  bool         `json:"hasLifetimeUnlock"`
	UpdateCutoffDate   *time.Time   `json:"updateCutoffDate"`
	OfflineGraceEndsAt *time.Time   `json:"offlineGraceEndsAt"`
	UnlockedFeatureIDs []FeatureID  `json:"unlockedFeatureIDs"`
}

// CanUseApp reports whether the app is usable (any reason other than locked).
func (d Decision) CanUseApp() bool { return d.Reason != ReasonLocked }

// IsTrialActive reports whether access is granted by an active trial.
func (d Decision) IsTrialActive() bool { return d.Reason == ReasonTrial }

// IsOfflineGraceActive reports whether access is granted by offline grace.
func (d Decision) IsOfflineGraceActive() bool { return d.Reason == ReasonOfflineGrace }

// CanUseFeature reports whether a specific feature is unlocked.
func (d Decision) CanUseFeature(id FeatureID) bool {
	if !d.CanUseApp() {
		return false
	}
	for _, f := range d.UnlockedFeatureIDs {
		if f == id {
			return true
		}
	}
	return false
}

// PaymentProvider identifies the checkout provider recorded for a license.
// Only Creem and NOWPayments are accepted purchase providers.
type PaymentProvider struct {
	// Raw is the canonical lowercase value ("creem", "nowpayments", or an
	// unsupported provider string preserved verbatim).
	Raw string
}

var (
	ProviderCreem       = PaymentProvider{Raw: "creem"}
	ProviderNOWPayments = PaymentProvider{Raw: "nowpayments"}
)

// AcceptedPurchaseProviders lists the only providers valid for a purchase.
func AcceptedPurchaseProviders() []PaymentProvider {
	return []PaymentProvider{ProviderCreem, ProviderNOWPayments}
}

// IsAccepted reports whether the provider is a valid purchase provider.
func (p PaymentProvider) IsAccepted() bool {
	return p.Raw == "creem" || p.Raw == "nowpayments"
}

// DisplayName returns the human label for the provider.
func (p PaymentProvider) DisplayName() string {
	switch p.Raw {
	case "creem":
		return "Creem"
	case "nowpayments":
		return "NOWPayments"
	default:
		return p.Raw
	}
}

// MarshalJSON encodes the provider as its raw string value.
func (p PaymentProvider) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.Raw)
}

// UnmarshalJSON decodes a provider string, lowercasing known providers.
func (p *PaymentProvider) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch strings.ToLower(value) {
	case "creem":
		p.Raw = "creem"
	case "nowpayments":
		p.Raw = "nowpayments"
	default:
		p.Raw = value
	}
	return nil
}

// DeviceRegistration describes the current device to the license backend.
type DeviceRegistration struct {
	InstallID    string `json:"install_id"`
	DisplayName  string `json:"display_name"`
	Platform     string `json:"platform"`
	Architecture string `json:"architecture"`
	AppVersion   string `json:"app_version,omitempty"`
}

// Device is a registered license seat as reported by the backend.
type Device struct {
	DeviceID      string     `json:"device_id"`
	InstallID     string     `json:"install_id"`
	DisplayName   string     `json:"display_name"`
	Platform      string     `json:"platform"`
	Architecture  string     `json:"architecture"`
	ActivatedAt   time.Time  `json:"activated_at"`
	LastSeenAt    *time.Time `json:"last_seen_at,omitempty"`
	DeactivatedAt *time.Time `json:"deactivated_at,omitempty"`
}

// IsActive reports whether the device seat is currently active.
func (d Device) IsActive() bool { return d.DeactivatedAt == nil }

// DeviceState is the device-seat inventory for a license.
type DeviceState struct {
	CurrentInstallID string           `json:"current_install_id"`
	CurrentDeviceID  string           `json:"current_device_id,omitempty"`
	MaxActiveDevices int              `json:"max_active_devices"`
	Devices          []Device         `json:"devices"`
	PaymentProvider  *PaymentProvider `json:"payment_provider,omitempty"`
}

// clampMax bounds the concurrent device limit to the contractual maximum.
func clampMax(n int) int {
	if n <= 0 {
		return 0
	}
	if n > MaxActiveDevices {
		return MaxActiveDevices
	}
	return n
}

// Normalized returns a copy with max_active_devices clamped to [0, 10] and a
// default applied when the backend omits it.
func (s DeviceState) Normalized() DeviceState {
	out := s
	if out.MaxActiveDevices == 0 {
		out.MaxActiveDevices = MaxActiveDevices
	}
	out.MaxActiveDevices = clampMax(out.MaxActiveDevices)
	return out
}

// ActiveDeviceCount counts non-deactivated seats.
func (s DeviceState) ActiveDeviceCount() int {
	n := 0
	for _, d := range s.Devices {
		if d.IsActive() {
			n++
		}
	}
	return n
}

// RemainingActivations is the number of seats still available.
func (s DeviceState) RemainingActivations() int {
	r := s.MaxActiveDevices - s.ActiveDeviceCount()
	if r < 0 {
		return 0
	}
	return r
}

// CurrentDevice resolves the seat for this install, by device ID when known,
// otherwise by matching the current install ID.
func (s DeviceState) CurrentDevice() *Device {
	if s.CurrentDeviceID != "" {
		for i := range s.Devices {
			if s.Devices[i].DeviceID == s.CurrentDeviceID {
				return &s.Devices[i]
			}
		}
	}
	for i := range s.Devices {
		if s.Devices[i].InstallID != "" && s.Devices[i].InstallID == s.CurrentInstallID {
			return &s.Devices[i]
		}
	}
	return nil
}

// IsCurrentDeviceActive reports whether this device holds an active seat.
func (s DeviceState) IsCurrentDeviceActive() bool {
	d := s.CurrentDevice()
	return d != nil && d.IsActive()
}

// CanActivateCurrentDevice reports whether this device can be (re)activated
// without exceeding the seat limit.
func (s DeviceState) CanActivateCurrentDevice() bool {
	return s.IsCurrentDeviceActive() || s.ActiveDeviceCount() < s.MaxActiveDevices
}

// CanReactivateCurrentDevice reports whether a deactivated current device can
// reclaim a seat.
func (s DeviceState) CanReactivateCurrentDevice() bool {
	if s.CurrentDevice() == nil || s.IsCurrentDeviceActive() {
		return false
	}
	return s.ActiveDeviceCount() < s.MaxActiveDevices
}

// CanTransferCurrentDevice reports whether this device holds a seat to release.
func (s DeviceState) CanTransferCurrentDevice() bool {
	return s.IsCurrentDeviceActive()
}

// WithCurrentInstallID returns a copy tagged with the current install ID.
func (s DeviceState) WithCurrentInstallID(installID string) DeviceState {
	out := s
	out.CurrentInstallID = installID
	return out
}

// ServerGrant is a signed license grant returned by the backend.
type ServerGrant struct {
	Version           int           `json:"version"`
	IssuedAt          time.Time     `json:"issued_at"`
	ExpiresAt         time.Time     `json:"expires_at"`
	Reason            AccessReason  `json:"reason"`
	TrialStartDate    *time.Time    `json:"trial_start_date,omitempty"`
	TrialEndsAt       *time.Time    `json:"trial_ends_at,omitempty"`
	HasLifetimeUnlock bool          `json:"has_lifetime_unlock"`
	UpdateCutoffDate  *time.Time    `json:"update_cutoff_date,omitempty"`
	Transactions      []Transaction `json:"transactions"`
	Signature         string        `json:"signature"`
}

// GrantSnapshot is the unsigned server view used to seed a local snapshot.
type GrantSnapshot struct {
	Reason            AccessReason  `json:"reason"`
	TrialStartDate    *time.Time    `json:"trial_start_date,omitempty"`
	TrialEndsAt       *time.Time    `json:"trial_ends_at,omitempty"`
	HasLifetimeUnlock bool          `json:"has_lifetime_unlock"`
	UpdateCutoffDate  *time.Time    `json:"update_cutoff_date,omitempty"`
	Transactions      []Transaction `json:"transactions"`
}

// LicenseSnapshot converts the server view into a locally cached snapshot,
// stamping verification timestamps at now.
func (g GrantSnapshot) LicenseSnapshot(now time.Time) Snapshot {
	return Snapshot{
		TrialStartDate: g.TrialStartDate,
		Transactions:   g.Transactions,
		LastVerifiedAt: &now,
		CachedAt:       now,
	}
}

// ServerResponse is the backend payload for every device endpoint.
type ServerResponse struct {
	Grant       ServerGrant   `json:"grant"`
	Snapshot    GrantSnapshot `json:"snapshot"`
	DeviceState DeviceState   `json:"device_state"`
}

// ActivationRequest is the body for the activate endpoint.
type ActivationRequest struct {
	LicenseKey string             `json:"license_key"`
	Email      string             `json:"email,omitempty"`
	Device     DeviceRegistration `json:"device"`
}

// DeviceActionRequest is the body for deactivate/reactivate/transfer.
type DeviceActionRequest struct {
	LicenseKey string             `json:"license_key"`
	InstallID  string             `json:"install_id"`
	DeviceID   string             `json:"device_id,omitempty"`
	Device     DeviceRegistration `json:"device"`
}
