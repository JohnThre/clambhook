package license

// Genuine-system verification trust root: Ed25519 signature verification of
// server grants and ban verdicts, plus the attestation build-token HMAC.
//
// The canonical form (the signed bytes) is a deterministic JSON string built by
// explicit field concatenation — identical byte-for-byte to the TypeScript
// signer in src/lib/clambhook-grant-signing.ts (swiphtgroup.com repo). Fields are
// in a fixed order, no whitespace, no trailing newline; dates are second-
// precision ISO-8601 UTC ("...Z"); null is the literal `null`; `signature` and
// `key_id` are excluded from the grant/ban canonical forms. The pinned sample
// CANONICAL_GRANT_SAMPLE is asserted by tests in BOTH repos so a divergence is
// caught immediately.
//
// A grant with an empty key_id is treated as a legacy/unsigned grant (accepted
// without verification) so the rollout is forward-compatible: old servers that
// emitted the placeholder signature keep working until the signing key is
// provisioned. A ban verdict MUST carry a key_id and a valid signature to be
// honored — a ban is never inferred from an unsigned message.

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CLAMBHOOKGrantKeyID is the key_id the production server signs with.
const CLAMBHOOKGrantKeyID = "clambhook-grant-v1"

// CANONICAL_GRANT_SAMPLE is the pinned canonical form for a known grant. Both
// this package's tests and the swiphtgroup.com grant-signing tests assert this
// exact string, so the Go and TS canonicalizers cannot silently diverge.
const CANONICAL_GRANT_SAMPLE = `{"version":1,"issued_at":"2026-06-10T00:00:00Z","expires_at":"2026-06-17T00:00:00Z","reason":"lifetime","trial_start_date":null,"trial_ends_at":null,"has_lifetime_unlock":true,"update_cutoff_date":"2027-06-10T00:00:00Z","transactions":[]}`

// Production public key for CLAMBHOOKGrantKeyID (Ed25519, 32 raw bytes, base64).
// The matching PRIVATE key is held as the CLAMBHOOK_GRANT_SIGNING_PRIVATE_KEY
// Wrangler secret in the swiphtgroup.com backend; only this public key is
// embedded in the client. Rotate by adding a new key_id + pubkey to the ring
// and signing with both during overlap.
const productionGrantPublicKeyB64 = "hDwdJ1ezLCFFjUMDSLZY2SMpbjUenwAbVtWL+w+tpR8="

// verifyingKeys maps key_id → Ed25519 public key. Tests inject additional keys
// via SetVerifyingKeyForTest (e.g. the test keypair) without weakening the
// production ring.
var verifyingKeys = func() map[string]ed25519.PublicKey {
	m := map[string]ed25519.PublicKey{}
	if key, err := decodePublicKey(productionGrantPublicKeyB64); err == nil {
		m[CLAMBHOOKGrantKeyID] = key
	}
	return m
}()

func decodePublicKey(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("license: ed25519 public key must be %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// SetVerifyingKeyForTest registers or replaces a verifying key for a key_id and
// returns a restore func. For tests only; production keys are compiled in. A
// malformed test key is not registered (the test's verification then fails
// visibly) rather than panicking.
func SetVerifyingKeyForTest(keyID string, publicKeyB64 string) (restore func()) {
	prev, had := verifyingKeys[keyID]
	key, err := decodePublicKey(publicKeyB64)
	if err != nil {
		return func() {}
	}
	verifyingKeys[keyID] = key
	return func() {
		if had {
			verifyingKeys[keyID] = prev
		} else {
			delete(verifyingKeys, keyID)
		}
	}
}

// ---------------------------------------------------------------------------
// Canonicalization — MUST match src/lib/clambhook-grant-signing.ts exactly.
// ---------------------------------------------------------------------------

func isoStr(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// jstr returns the JSON string encoding of s (handles quoting/escaping),
// matching JSON.stringify in the TS canonicalizer.
func jstr(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		// json.Marshal on a string never errors in practice.
		return strconv.Quote(s)
	}
	return string(b)
}

func jNullOrTime(t *time.Time) string {
	if t == nil {
		return "null"
	}
	return jstr(isoStr(*t))
}

func jNullOrStr(s *string) string {
	if s == nil {
		return "null"
	}
	return jstr(*s)
}

// CanonicalizeGrant returns the canonical message the server signs for a grant.
// Excludes signature and key_id.
func CanonicalizeGrant(g ServerGrant) string {
	var b strings.Builder
	b.WriteString(`{"version":`)
	b.WriteString(strconv.Itoa(g.Version))
	b.WriteString(`,"issued_at":`)
	b.WriteString(jstr(isoStr(g.IssuedAt)))
	b.WriteString(`,"expires_at":`)
	b.WriteString(jstr(isoStr(g.ExpiresAt)))
	b.WriteString(`,"reason":`)
	b.WriteString(jstr(string(g.Reason)))
	b.WriteString(`,"trial_start_date":`)
	b.WriteString(jNullOrTime(g.TrialStartDate))
	b.WriteString(`,"trial_ends_at":`)
	b.WriteString(jNullOrTime(g.TrialEndsAt))
	b.WriteString(`,"has_lifetime_unlock":`)
	b.WriteString(strconv.FormatBool(g.HasLifetimeUnlock))
	b.WriteString(`,"update_cutoff_date":`)
	b.WriteString(jNullOrTime(g.UpdateCutoffDate))
	b.WriteString(`,"transactions":`)
	b.WriteString(canonicalTransactions(g.Transactions))
	b.WriteString(`}`)
	return b.String()
}

func canonicalTransactions(txns []Transaction) string {
	if len(txns) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, t := range txns {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"productID":`)
		b.WriteString(jstr(t.ProductID))
		b.WriteString(`,"purchaseDate":`)
		b.WriteString(jstr(isoStr(t.PurchaseDate)))
		b.WriteString(`,"revocationDate":`)
		b.WriteString(jNullOrTime(t.RevocationDate))
		b.WriteString(`}`)
	}
	b.WriteByte(']')
	return b.String()
}

// CanonicalizeBan returns the canonical message the server signs for a ban
// verdict. Excludes key_id and signature.
func CanonicalizeBan(m BanMarker) string {
	var b strings.Builder
	b.WriteString(`{"ban_id":`)
	b.WriteString(jstr(m.BanID))
	b.WriteString(`,"reason":`)
	b.WriteString(jstr(m.Reason))
	b.WriteString(`,"source":`)
	b.WriteString(jstr(m.Source))
	b.WriteString(`,"banned_at":`)
	b.WriteString(jstr(isoStr(m.BannedAt)))
	b.WriteString(`,"expires_at":`)
	b.WriteString(jNullOrTime(m.ExpiresAt))
	b.WriteString(`,"install_id":`)
	b.WriteString(jstr(m.InstallID))
	b.WriteString(`,"license_key_hash":`)
	b.WriteString(jNullOrStr(m.LicenseKeyHash))
	b.WriteString(`,"device_id":`)
	b.WriteString(jNullOrStr(m.DeviceID))
	b.WriteString(`}`)
	return b.String()
}

// CanonicalizeAttestation returns the canonical message the build_token HMAC
// covers. MUST match canonicalizeAttestation in the TS grant-signing lib.
func CanonicalizeAttestation(keyID, installID, binaryHash string, codeSignIdentity *string, nonce string, ts int64) string {
	var b strings.Builder
	b.WriteString(`{"key_id":`)
	b.WriteString(jstr(keyID))
	b.WriteString(`,"install_id":`)
	b.WriteString(jstr(installID))
	b.WriteString(`,"binary_hash":`)
	b.WriteString(jstr(binaryHash))
	b.WriteString(`,"code_sign_identity":`)
	b.WriteString(jNullOrStr(codeSignIdentity))
	b.WriteString(`,"nonce":`)
	b.WriteString(jstr(nonce))
	b.WriteString(`,"timestamp":`)
	b.WriteString(strconv.FormatInt(ts, 10))
	b.WriteString(`}`)
	return b.String()
}

// ---------------------------------------------------------------------------
// Verification
// ---------------------------------------------------------------------------

// ErrUnsignedGrant is returned when a grant carries a key_id but no signature.
var ErrUnsignedGrant = errors.New("license: grant has key_id but no signature")

// VerifyGrant verifies a server grant's Ed25519 signature. A grant with an
// empty key_id is accepted as a legacy/unsigned grant (returns nil) for
// forward compatibility during the rollout.
func VerifyGrant(g ServerGrant) error {
	if g.KeyID == "" {
		return nil
	}
	if g.Signature == "" {
		return ErrUnsignedGrant
	}
	pubkey, ok := verifyingKeys[g.KeyID]
	if !ok {
		return fmt.Errorf("license: unknown grant key_id %q", g.KeyID)
	}
	sig, err := base64.StdEncoding.DecodeString(g.Signature)
	if err != nil {
		return fmt.Errorf("license: grant signature decode: %w", err)
	}
	if !ed25519.Verify(pubkey, []byte(CanonicalizeGrant(g)), sig) {
		return errors.New("license: grant signature invalid")
	}
	return nil
}

// VerifyBanMarker verifies a ban verdict's Ed25519 signature and that it is
// bound to this device's install_id. A ban must carry a key_id and a valid
// signature; an unsigned or tampered marker is rejected (and thus ignored by
// Evaluate, so a local tamper cannot lock the app).
func VerifyBanMarker(m BanMarker, installID string) error {
	if m.KeyID == "" {
		return errors.New("license: ban verdict has no key_id")
	}
	if m.Signature == "" {
		return errors.New("license: ban verdict has no signature")
	}
	if installID != "" && m.InstallID != "" && m.InstallID != installID {
		return fmt.Errorf("license: ban verdict is for a different install_id")
	}
	pubkey, ok := verifyingKeys[m.KeyID]
	if !ok {
		return fmt.Errorf("license: unknown ban key_id %q", m.KeyID)
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return fmt.Errorf("license: ban signature decode: %w", err)
	}
	if !ed25519.Verify(pubkey, []byte(CanonicalizeBan(m)), sig) {
		return errors.New("license: ban verdict signature invalid")
	}
	return nil
}

// BuildAttestationToken computes the HMAC-SHA256 build_token the client sends
// in an attestation, proving the binary carries the build-embedded secret. The
// secret is injected at build time (not committed); an empty secret yields an
// empty token and the server rejects it (dev/CI attests then fail closed into
// offline grace).
func BuildAttestationToken(keyID, installID, binaryHash string, codeSignIdentity *string, nonce string, ts int64, buildSecret string) string {
	mac := hmac.New(sha256.New, []byte(buildSecret))
	mac.Write([]byte(CanonicalizeAttestation(keyID, installID, binaryHash, codeSignIdentity, nonce, ts)))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
