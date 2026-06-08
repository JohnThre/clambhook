package license

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

type StoreKitJWSValidator struct {
	roots *x509.CertPool
}

func NewStoreKitJWSValidator(cfg Config) (*StoreKitJWSValidator, error) {
	roots := x509.NewCertPool()
	if len(cfg.AppleRootsPEM) > 0 {
		rest := cfg.AppleRootsPEM
		for {
			var block *pem.Block
			block, rest = pem.Decode(rest)
			if block == nil {
				break
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("parse StoreKit root: %w", err)
			}
			roots.AddCert(cert)
		}
	}
	return &StoreKitJWSValidator{roots: roots}, nil
}

type jwsHeader struct {
	Algorithm string   `json:"alg"`
	X5C       []string `json:"x5c"`
}

type storeKitTransactionPayload struct {
	TransactionID      string `json:"transactionId"`
	OriginalID         string `json:"originalTransactionId"`
	ProductID          string `json:"productId"`
	PurchaseDate       int64  `json:"purchaseDate"`
	RevocationDate     *int64 `json:"revocationDate"`
	InAppOwnershipType string `json:"inAppOwnershipType"`
	AppAccountToken    string `json:"appAccountToken"`
}

func (v *StoreKitJWSValidator) Validate(jws string) (LicenseTransaction, error) {
	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		return LicenseTransaction{}, errors.New("transaction JWS must have three parts")
	}
	headerData, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return LicenseTransaction{}, errors.New("invalid JWS header encoding")
	}
	payloadData, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return LicenseTransaction{}, errors.New("invalid JWS payload encoding")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return LicenseTransaction{}, errors.New("invalid JWS signature encoding")
	}
	var header jwsHeader
	if err := json.Unmarshal(headerData, &header); err != nil {
		return LicenseTransaction{}, fmt.Errorf("parse JWS header: %w", err)
	}
	if header.Algorithm != "ES256" {
		return LicenseTransaction{}, errors.New("unsupported JWS algorithm")
	}
	if len(header.X5C) == 0 {
		return LicenseTransaction{}, errors.New("JWS certificate chain missing")
	}
	certs, err := decodeX5C(header.X5C)
	if err != nil {
		return LicenseTransaction{}, err
	}
	if v.roots != nil && len(v.roots.Subjects()) > 0 {
		intermediates := x509.NewCertPool()
		for _, cert := range certs[1:] {
			intermediates.AddCert(cert)
		}
		if _, err := certs[0].Verify(x509.VerifyOptions{Roots: v.roots, Intermediates: intermediates}); err != nil {
			return LicenseTransaction{}, fmt.Errorf("verify StoreKit certificate chain: %w", err)
		}
	}
	publicKey, ok := certs[0].PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return LicenseTransaction{}, errors.New("StoreKit JWS leaf key is not ECDSA")
	}
	signedData := []byte(parts[0] + "." + parts[1])
	digest := sha256.Sum256(signedData)
	if !verifyES256Raw(publicKey, digest[:], signature) {
		return LicenseTransaction{}, errors.New("StoreKit JWS signature invalid")
	}

	var payload storeKitTransactionPayload
	if err := json.Unmarshal(payloadData, &payload); err != nil {
		return LicenseTransaction{}, fmt.Errorf("parse StoreKit transaction payload: %w", err)
	}
	if !isKnownProduct(payload.ProductID) {
		return LicenseTransaction{}, fmt.Errorf("unknown product id %q", payload.ProductID)
	}
	purchaseDate := time.UnixMilli(payload.PurchaseDate).UTC()
	var revocationDate *time.Time
	if payload.RevocationDate != nil {
		value := time.UnixMilli(*payload.RevocationDate).UTC()
		revocationDate = &value
	}
	ownership := "purchased"
	if strings.EqualFold(payload.InAppOwnershipType, "FAMILY_SHARED") {
		ownership = "familyShared"
	}
	txID := payload.TransactionID
	if txID == "" {
		txID = payload.OriginalID
	}
	return LicenseTransaction{
		ProductID:      payload.ProductID,
		PurchaseDate:   purchaseDate,
		RevocationDate: revocationDate,
		OwnershipType:  ownership,
		TransactionID:  txID,
	}, nil
}

func decodeX5C(values []string) ([]*x509.Certificate, error) {
	certs := make([]*x509.Certificate, 0, len(values))
	for _, value := range values {
		der, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, errors.New("invalid x5c certificate encoding")
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("parse x5c certificate: %w", err)
		}
		certs = append(certs, cert)
	}
	return certs, nil
}

func verifyES256Raw(key *ecdsa.PublicKey, digest, signature []byte) bool {
	if len(signature) != 64 {
		return false
	}
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	return ecdsa.Verify(key, digest, r, s)
}
