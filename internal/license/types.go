package license

import (
	"crypto/ecdsa"
	"time"
)

const (
	LifetimeUnlockProductID = "org.jpfchang.clambhook.unlock.lifetime"
	FeatureUpdatePrefix     = "org.jpfchang.clambhook.feature_update."
	TrialMonths             = 2

	AccessReasonTrial        = "trial"
	AccessReasonLifetime     = "lifetime"
	AccessReasonOfflineGrace = "offlineGrace"
	AccessReasonLocked       = "locked"

	ChallengePurposeAttest   = "attest"
	ChallengePurposeValidate = "validate"
)

type Config struct {
	AppID              string
	AppAppleID         int64
	Environment        string
	HMACSecret         []byte
	GrantSigningSecret []byte
	AppleRootsPEM      []byte
	OfflineGrace       time.Duration
	MaxAttestations30d int
	Now                func() time.Time
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

type ChallengeRequest struct {
	Purpose   string `json:"purpose"`
	InstallID string `json:"install_id,omitempty"`
	KeyID     string `json:"key_id,omitempty"`
}

type ChallengeResponse struct {
	ChallengeID string    `json:"challenge_id"`
	Challenge   string    `json:"challenge"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type AttestRequest struct {
	ChallengeID       string `json:"challenge_id"`
	InstallID         string `json:"install_id"`
	KeyID             string `json:"key_id"`
	AttestationObject string `json:"attestation_object"`
}

type ValidateRequest struct {
	KeyID        string   `json:"key_id"`
	ClientData   string   `json:"client_data"`
	Assertion    string   `json:"assertion"`
	Transactions []string `json:"transactions,omitempty"`
}

type AssertionClientData struct {
	ChallengeID  string   `json:"challenge_id"`
	Challenge    string   `json:"challenge"`
	InstallID    string   `json:"install_id"`
	KeyID        string   `json:"key_id"`
	Transactions []string `json:"transactions,omitempty"`
}

type GrantResponse struct {
	Grant    LicenseGrant  `json:"grant"`
	Snapshot GrantSnapshot `json:"snapshot"`
}

type GrantSnapshot struct {
	Reason             string               `json:"reason"`
	TrialStartDate     *time.Time           `json:"trial_start_date,omitempty"`
	TrialEndsAt        *time.Time           `json:"trial_ends_at,omitempty"`
	HasLifetimeUnlock  bool                 `json:"has_lifetime_unlock"`
	UpdateCutoffDate   *time.Time           `json:"update_cutoff_date,omitempty"`
	OfflineGraceEndsAt *time.Time           `json:"offline_grace_ends_at,omitempty"`
	Transactions       []LicenseTransaction `json:"transactions"`
}

type LicenseGrant struct {
	Version            int                  `json:"version"`
	IssuedAt           time.Time            `json:"issued_at"`
	ExpiresAt          time.Time            `json:"expires_at"`
	InstallHash        string               `json:"install_hash"`
	KeyHash            string               `json:"key_hash"`
	Reason             string               `json:"reason"`
	TrialStartDate     *time.Time           `json:"trial_start_date,omitempty"`
	TrialEndsAt        *time.Time           `json:"trial_ends_at,omitempty"`
	HasLifetimeUnlock  bool                 `json:"has_lifetime_unlock"`
	UpdateCutoffDate   *time.Time           `json:"update_cutoff_date,omitempty"`
	OfflineGraceEndsAt *time.Time           `json:"offline_grace_ends_at,omitempty"`
	Transactions       []LicenseTransaction `json:"transactions"`
	Signature          string               `json:"signature"`
}

type LicenseTransaction struct {
	ProductID       string     `json:"product_id"`
	PurchaseDate    time.Time  `json:"purchase_date"`
	RevocationDate  *time.Time `json:"revocation_date,omitempty"`
	OwnershipType   string     `json:"ownership_type"`
	TransactionID   string     `json:"transaction_id"`
	BundleID        string     `json:"-"`
	AppAppleID      int64      `json:"-"`
	Environment     string     `json:"-"`
	AppAccountToken string     `json:"-"`
}

type ChallengeRecord struct {
	ID          string     `json:"id"`
	Purpose     string     `json:"purpose"`
	Challenge   string     `json:"challenge"`
	InstallHash string     `json:"install_hash"`
	KeyHash     string     `json:"key_hash"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	ConsumedAt  *time.Time `json:"consumed_at,omitempty"`
}

type DeviceRecord struct {
	InstallHash        string    `json:"install_hash"`
	KeyHash            string    `json:"key_hash"`
	KeyID              string    `json:"key_id"`
	PublicKeyDER       []byte    `json:"public_key_der"`
	Receipt            []byte    `json:"receipt"`
	ReceiptMetric      *int      `json:"receipt_metric,omitempty"`
	AssertionCounter   uint32    `json:"assertion_counter"`
	TrialStartDate     time.Time `json:"trial_start_date"`
	TrialEndsAt        time.Time `json:"trial_ends_at"`
	LastVerifiedAt     time.Time `json:"last_verified_at"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	AttestationEnv     string    `json:"attestation_env"`
	TransactionIDHashs []string  `json:"transaction_id_hashes,omitempty"`

	publicKey *ecdsa.PublicKey
}

type AttestationResult struct {
	PublicKey    *ecdsa.PublicKey
	PublicKeyDER []byte
	Receipt      []byte
	Counter      uint32
	Environment  string
}

type TransactionValidator interface {
	Validate(jws string) (LicenseTransaction, error)
}

type AppAttestValidator interface {
	ValidateAttestation(keyID string, challenge []byte, attestationObject []byte) (AttestationResult, error)
	ValidateAssertion(publicKey *ecdsa.PublicKey, previousCounter uint32, clientData []byte, assertionObject []byte) (uint32, error)
}

type ReceiptRiskAssessor interface {
	Assess(receipt []byte) (ReceiptAssessment, error)
}

type ReceiptAssessment struct {
	Receipt []byte
	Metric  *int
}
