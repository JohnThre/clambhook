package license

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServerAttestationStartsTrialAndRejectsReplay(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	server := newTestServer(t, now, LicenseTransaction{})

	challenge := postJSON[ChallengeResponse](t, server, "/v1/license/challenge", ChallengeRequest{
		Purpose:   ChallengePurposeAttest,
		InstallID: "install-a",
		KeyID:     "key-a",
	}, http.StatusOK)
	req := AttestRequest{
		ChallengeID:       challenge.ChallengeID,
		InstallID:         "install-a",
		KeyID:             "key-a",
		AttestationObject: base64.StdEncoding.EncodeToString([]byte("attestation")),
	}
	grant := postJSON[GrantResponse](t, server, "/v1/license/attest", req, http.StatusOK)
	if grant.Snapshot.Reason != AccessReasonTrial {
		t.Fatalf("reason = %q, want trial", grant.Snapshot.Reason)
	}
	if grant.Snapshot.TrialEndsAt == nil || !grant.Snapshot.TrialEndsAt.Equal(time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("trial end = %v", grant.Snapshot.TrialEndsAt)
	}

	postJSON[map[string]string](t, server, "/v1/license/attest", req, http.StatusConflict)
}

func TestTrialEndDateUsesTwoCalendarMonthsClampedToTargetMonth(t *testing.T) {
	tests := []struct {
		name  string
		start time.Time
		want  time.Time
	}{
		{
			name:  "same day when target month has day",
			start: time.Date(2026, 6, 3, 12, 30, 5, 9, time.UTC),
			want:  time.Date(2026, 8, 3, 12, 30, 5, 9, time.UTC),
		},
		{
			name:  "end of month preserved when possible",
			start: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
			want:  time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			name:  "target month clamps in common year",
			start: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
			want:  time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC),
		},
		{
			name:  "target month clamps in leap year",
			start: time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC),
			want:  time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trialEndDate(tt.start); !got.Equal(tt.want) {
				t.Fatalf("trialEndDate(%v) = %v, want %v", tt.start, got, tt.want)
			}
		})
	}
}

func TestServerValidationReturnsLifetimeGrant(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	tx := LicenseTransaction{
		ProductID:     LifetimeUnlockProductID,
		PurchaseDate:  now,
		OwnershipType: "purchased",
		TransactionID: "tx-1",
	}
	server := newTestServer(t, now, tx)

	attestChallenge := postJSON[ChallengeResponse](t, server, "/v1/license/challenge", ChallengeRequest{
		Purpose:   ChallengePurposeAttest,
		InstallID: "install-a",
		KeyID:     "key-a",
	}, http.StatusOK)
	postJSON[GrantResponse](t, server, "/v1/license/attest", AttestRequest{
		ChallengeID:       attestChallenge.ChallengeID,
		InstallID:         "install-a",
		KeyID:             "key-a",
		AttestationObject: base64.StdEncoding.EncodeToString([]byte("attestation")),
	}, http.StatusOK)

	validateChallenge := postJSON[ChallengeResponse](t, server, "/v1/license/challenge", ChallengeRequest{
		Purpose:   ChallengePurposeValidate,
		InstallID: "install-a",
		KeyID:     "key-a",
	}, http.StatusOK)
	clientData := AssertionClientData{
		ChallengeID:  validateChallenge.ChallengeID,
		Challenge:    validateChallenge.Challenge,
		InstallID:    "install-a",
		KeyID:        "key-a",
		Transactions: []string{"signed-tx"},
	}
	clientDataJSON, err := json.Marshal(clientData)
	if err != nil {
		t.Fatal(err)
	}
	grant := postJSON[GrantResponse](t, server, "/v1/license/validate", ValidateRequest{
		KeyID:        "key-a",
		ClientData:   base64.StdEncoding.EncodeToString(clientDataJSON),
		Assertion:    base64.StdEncoding.EncodeToString([]byte("assertion")),
		Transactions: []string{"signed-tx"},
	}, http.StatusOK)

	if grant.Snapshot.Reason != AccessReasonTrial {
		t.Fatalf("reason during active trial = %q, want trial", grant.Snapshot.Reason)
	}
	if !grant.Snapshot.HasLifetimeUnlock {
		t.Fatal("expected lifetime unlock")
	}
	if grant.Snapshot.UpdateCutoffDate == nil || !grant.Snapshot.UpdateCutoffDate.Equal(now.AddDate(1, 0, 0)) {
		t.Fatalf("cutoff = %v", grant.Snapshot.UpdateCutoffDate)
	}
}

func newTestServer(t *testing.T, now time.Time, tx LicenseTransaction) *Server {
	t.Helper()
	cfg := Config{
		AppID:              "TEAMID.org.jpfchang.clambhook",
		Environment:        "development",
		HMACSecret:         []byte("0123456789abcdef0123456789abcdef"),
		GrantSigningSecret: []byte("abcdef0123456789abcdef0123456789"),
		OfflineGrace:       7 * 24 * time.Hour,
		Now:                func() time.Time { return now },
	}
	server, err := NewServer(
		cfg,
		NewMemoryStore(),
		newFakeAppAttest(t),
		fakeTransactionValidator{tx: tx},
		NoopReceiptRiskAssessor{},
	)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func postJSON[T any](t *testing.T, handler http.Handler, path string, body any, wantStatus int) T {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s status = %d, want %d, body %s", path, rec.Code, wantStatus, rec.Body.String())
	}
	var out T
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return out
}

type fakeAppAttest struct {
	private *ecdsa.PrivateKey
}

func newFakeAppAttest(t *testing.T) fakeAppAttest {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return fakeAppAttest{private: private}
}

func (f fakeAppAttest) ValidateAttestation(string, []byte, []byte) (AttestationResult, error) {
	der, err := x509.MarshalPKIXPublicKey(&f.private.PublicKey)
	if err != nil {
		return AttestationResult{}, err
	}
	return AttestationResult{
		PublicKey:    &f.private.PublicKey,
		PublicKeyDER: der,
		Receipt:      []byte("receipt"),
		Counter:      0,
		Environment:  "development",
	}, nil
}

func (f fakeAppAttest) ValidateAssertion(*ecdsa.PublicKey, uint32, []byte, []byte) (uint32, error) {
	return 1, nil
}

type fakeTransactionValidator struct {
	tx LicenseTransaction
}

func (f fakeTransactionValidator) Validate(string) (LicenseTransaction, error) {
	return f.tx, nil
}
