package license

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

const storeKitTestAppAccountToken = "550e8400-e29b-41d4-a716-446655440000"

func TestStoreKitJWSValidatorValidatesIdentityAndTransactionFields(t *testing.T) {
	validator, key, cert := newStoreKitTestValidator(t, Config{
		AppID:       "TEAMID.org.jpfchang.clambhook",
		AppAppleID:  1234567890,
		Environment: "production",
	})
	revokedAt := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC).UnixMilli()
	payload := validStoreKitPayload()
	payload.InAppOwnershipType = "FAMILY_SHARED"
	payload.RevocationDate = &revokedAt

	tx, err := validator.Validate(signStoreKitTestJWS(t, key, cert, payload))
	if err != nil {
		t.Fatal(err)
	}
	if tx.ProductID != LifetimeUnlockProductID {
		t.Fatalf("product id = %q", tx.ProductID)
	}
	if tx.BundleID != "org.jpfchang.clambhook" {
		t.Fatalf("bundle id = %q", tx.BundleID)
	}
	if tx.AppAppleID != 1234567890 {
		t.Fatalf("app apple id = %d", tx.AppAppleID)
	}
	if tx.Environment != "Production" {
		t.Fatalf("environment = %q", tx.Environment)
	}
	if tx.OwnershipType != "familyShared" {
		t.Fatalf("ownership = %q", tx.OwnershipType)
	}
	if tx.RevocationDate == nil || !tx.RevocationDate.Equal(time.UnixMilli(revokedAt).UTC()) {
		t.Fatalf("revocation date = %v", tx.RevocationDate)
	}
	if tx.AppAccountToken != storeKitTestAppAccountToken {
		t.Fatalf("app account token = %q", tx.AppAccountToken)
	}
}

func TestStoreKitJWSValidatorRejectsInvalidPayloadChecks(t *testing.T) {
	validator, key, cert := newStoreKitTestValidator(t, Config{
		AppID:       "TEAMID.org.jpfchang.clambhook",
		AppAppleID:  1234567890,
		Environment: "production",
	})

	tests := []struct {
		name      string
		mutate    func(*storeKitTransactionPayload)
		wantError string
	}{
		{
			name: "bundle id mismatch",
			mutate: func(payload *storeKitTransactionPayload) {
				payload.BundleID = "org.example.other"
			},
			wantError: "bundle id",
		},
		{
			name: "app apple id mismatch",
			mutate: func(payload *storeKitTransactionPayload) {
				payload.AppAppleID = 42
			},
			wantError: "app apple id",
		},
		{
			name: "environment mismatch",
			mutate: func(payload *storeKitTransactionPayload) {
				payload.Environment = "Sandbox"
			},
			wantError: "environment",
		},
		{
			name: "unknown product",
			mutate: func(payload *storeKitTransactionPayload) {
				payload.ProductID = "org.jpfchang.clambhook.other"
			},
			wantError: "unknown product",
		},
		{
			name: "unsupported ownership",
			mutate: func(payload *storeKitTransactionPayload) {
				payload.InAppOwnershipType = "UNKNOWN"
			},
			wantError: "ownership",
		},
		{
			name: "missing app account token",
			mutate: func(payload *storeKitTransactionPayload) {
				payload.AppAccountToken = ""
			},
			wantError: "app account token",
		},
		{
			name: "revocation before purchase",
			mutate: func(payload *storeKitTransactionPayload) {
				revokedAt := time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC).UnixMilli()
				payload.RevocationDate = &revokedAt
			},
			wantError: "revocation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := validStoreKitPayload()
			tt.mutate(&payload)
			_, err := validator.Validate(signStoreKitTestJWS(t, key, cert, payload))
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.wantError)) {
				t.Fatalf("error = %q, want containing %q", err.Error(), tt.wantError)
			}
		})
	}
}

func TestStoreKitJWSValidatorAllowsSandboxForDevelopment(t *testing.T) {
	validator, key, cert := newStoreKitTestValidator(t, Config{
		AppID:       "TEAMID.org.jpfchang.clambhook",
		Environment: "development",
	})
	payload := validStoreKitPayload()
	payload.Environment = "Sandbox"
	payload.AppAppleID = 0

	if _, err := validator.Validate(signStoreKitTestJWS(t, key, cert, payload)); err != nil {
		t.Fatal(err)
	}
}

func validStoreKitPayload() storeKitTransactionPayload {
	return storeKitTransactionPayload{
		TransactionID:      "tx-1",
		OriginalID:         "orig-1",
		BundleID:           "org.jpfchang.clambhook",
		AppAppleID:         1234567890,
		Environment:        "Production",
		ProductID:          LifetimeUnlockProductID,
		PurchaseDate:       time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC).UnixMilli(),
		InAppOwnershipType: "PURCHASED",
		AppAccountToken:    storeKitTestAppAccountToken,
	}
}

func newStoreKitTestValidator(t *testing.T, cfg Config) (*StoreKitJWSValidator, *ecdsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "StoreKit Test Root"},
		NotBefore:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AppleRootsPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	validator, err := NewStoreKitJWSValidator(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return validator, key, cert
}

func signStoreKitTestJWS(t *testing.T, key *ecdsa.PrivateKey, cert *x509.Certificate, payload storeKitTransactionPayload) string {
	t.Helper()
	header := jwsHeader{
		Algorithm: "ES256",
		X5C:       []string{base64.StdEncoding.EncodeToString(cert.Raw)},
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(headerJSON)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signedData := []byte(encodedHeader + "." + encodedPayload)
	digest := sha256.Sum256(signedData)
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := append(paddedP256Scalar(r), paddedP256Scalar(s)...)
	return encodedHeader + "." + encodedPayload + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func paddedP256Scalar(value *big.Int) []byte {
	out := make([]byte, 32)
	bytes := value.Bytes()
	copy(out[len(out)-len(bytes):], bytes)
	return out
}
