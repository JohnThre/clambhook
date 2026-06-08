package license

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
)

var (
	appAttestNonceOID = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 2}
	productionAAGUID  = []byte{'a', 'p', 'p', 'a', 't', 't', 'e', 's', 't', 0, 0, 0, 0, 0, 0, 0}
	developmentAAGUID = []byte("appattestdevelop")
	sandboxAAGUID     = []byte("appattestsandbox")
)

type AppleAppAttestValidator struct {
	appID       string
	environment string
	roots       *x509.CertPool
}

func NewAppleAppAttestValidator(cfg Config) (*AppleAppAttestValidator, error) {
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
				return nil, fmt.Errorf("parse App Attest root: %w", err)
			}
			roots.AddCert(cert)
		}
	}
	return &AppleAppAttestValidator{
		appID:       cfg.AppID,
		environment: cfg.Environment,
		roots:       roots,
	}, nil
}

func (v *AppleAppAttestValidator) ValidateAttestation(keyID string, challenge []byte, attestationObject []byte) (AttestationResult, error) {
	decoded, err := decodeCBOR(attestationObject)
	if err != nil {
		return AttestationResult{}, fmt.Errorf("decode attestation CBOR: %w", err)
	}
	root, ok := cborMap(decoded)
	if !ok {
		return AttestationResult{}, errors.New("attestation object must be a CBOR map")
	}
	format, ok := cborString(root["fmt"])
	if !ok || format != "apple-appattest" {
		return AttestationResult{}, errors.New("attestation format is not apple-appattest")
	}
	authData, ok := cborBytes(root["authData"])
	if !ok {
		return AttestationResult{}, errors.New("attestation authData missing")
	}
	stmt, ok := cborMap(root["attStmt"])
	if !ok {
		return AttestationResult{}, errors.New("attestation statement missing")
	}
	x5cValues, ok := cborArray(stmt["x5c"])
	if !ok || len(x5cValues) == 0 {
		return AttestationResult{}, errors.New("attestation certificate chain missing")
	}
	certs := make([]*x509.Certificate, 0, len(x5cValues))
	for _, raw := range x5cValues {
		der, ok := cborBytes(raw)
		if !ok {
			return AttestationResult{}, errors.New("attestation certificate is not bytes")
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return AttestationResult{}, fmt.Errorf("parse attestation certificate: %w", err)
		}
		certs = append(certs, cert)
	}
	if v.roots != nil && len(v.roots.Subjects()) > 0 {
		intermediates := x509.NewCertPool()
		for _, cert := range certs[1:] {
			intermediates.AddCert(cert)
		}
		if _, err := certs[0].Verify(x509.VerifyOptions{Roots: v.roots, Intermediates: intermediates}); err != nil {
			return AttestationResult{}, fmt.Errorf("verify App Attest certificate chain: %w", err)
		}
	}

	clientHash := sha256.Sum256(challenge)
	nonceBytes := append([]byte{}, authData...)
	nonceBytes = append(nonceBytes, clientHash[:]...)
	nonce := sha256.Sum256(nonceBytes)
	if err := verifyCertificateNonce(certs[0], nonce[:]); err != nil {
		return AttestationResult{}, err
	}
	parsedAuth, err := parseAttestationAuthData(authData)
	if err != nil {
		return AttestationResult{}, err
	}
	expectedRPID := sha256.Sum256([]byte(v.appID))
	if !bytes.Equal(parsedAuth.rpIDHash, expectedRPID[:]) {
		return AttestationResult{}, errors.New("attestation app id hash mismatch")
	}
	if parsedAuth.counter != 0 {
		return AttestationResult{}, errors.New("attestation counter must be zero")
	}
	env, err := aaguidEnvironment(parsedAuth.aaguid)
	if err != nil {
		return AttestationResult{}, err
	}
	if v.environment == "production" && env != "production" {
		return AttestationResult{}, errors.New("production service requires production App Attest AAGUID")
	}
	keyBytes, err := base64.StdEncoding.DecodeString(keyID)
	if err != nil {
		return AttestationResult{}, errors.New("key id must be base64")
	}
	if !bytes.Equal(parsedAuth.credentialID, keyBytes) {
		return AttestationResult{}, errors.New("attestation credential id mismatch")
	}
	publicKey, ok := certs[0].PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return AttestationResult{}, errors.New("attestation certificate public key is not ECDSA")
	}
	keyHash := sha256.Sum256(elliptic.Marshal(publicKey.Curve, publicKey.X, publicKey.Y))
	if !bytes.Equal(keyHash[:], keyBytes) {
		return AttestationResult{}, errors.New("attestation key id does not match public key")
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return AttestationResult{}, fmt.Errorf("marshal public key: %w", err)
	}
	receipt, _ := cborBytes(stmt["receipt"])
	return AttestationResult{
		PublicKey:    publicKey,
		PublicKeyDER: publicKeyDER,
		Receipt:      receipt,
		Counter:      parsedAuth.counter,
		Environment:  env,
	}, nil
}

func (v *AppleAppAttestValidator) ValidateAssertion(publicKey *ecdsa.PublicKey, previousCounter uint32, clientData []byte, assertionObject []byte) (uint32, error) {
	decoded, err := decodeCBOR(assertionObject)
	if err != nil {
		return 0, fmt.Errorf("decode assertion CBOR: %w", err)
	}
	root, ok := cborMap(decoded)
	if !ok {
		return 0, errors.New("assertion object must be a CBOR map")
	}
	signature, ok := cborBytes(root["signature"])
	if !ok {
		return 0, errors.New("assertion signature missing")
	}
	authData, ok := cborBytes(root["authenticatorData"])
	if !ok {
		return 0, errors.New("assertion authenticatorData missing")
	}
	parsed, err := parseAssertionAuthData(authData)
	if err != nil {
		return 0, err
	}
	expectedRPID := sha256.Sum256([]byte(v.appID))
	if !bytes.Equal(parsed.rpIDHash, expectedRPID[:]) {
		return 0, errors.New("assertion app id hash mismatch")
	}
	if parsed.counter <= previousCounter {
		return 0, errors.New("assertion counter did not increase")
	}
	clientHash := sha256.Sum256(clientData)
	nonceBytes := append([]byte{}, authData...)
	nonceBytes = append(nonceBytes, clientHash[:]...)
	nonce := sha256.Sum256(nonceBytes)
	if !ecdsa.VerifyASN1(publicKey, nonce[:], signature) {
		return 0, errors.New("assertion signature invalid")
	}
	return parsed.counter, nil
}

type attestationAuthData struct {
	rpIDHash     []byte
	counter      uint32
	aaguid       []byte
	credentialID []byte
}

type assertionAuthData struct {
	rpIDHash []byte
	counter  uint32
}

func parseAssertionAuthData(authData []byte) (assertionAuthData, error) {
	if len(authData) < 37 {
		return assertionAuthData{}, errors.New("authenticator data too short")
	}
	return assertionAuthData{
		rpIDHash: authData[:32],
		counter:  binary.BigEndian.Uint32(authData[33:37]),
	}, nil
}

func parseAttestationAuthData(authData []byte) (attestationAuthData, error) {
	if len(authData) < 55 {
		return attestationAuthData{}, errors.New("attestation authData too short")
	}
	out := attestationAuthData{
		rpIDHash: authData[:32],
		counter:  binary.BigEndian.Uint32(authData[33:37]),
		aaguid:   authData[37:53],
	}
	credLen := int(binary.BigEndian.Uint16(authData[53:55]))
	if len(authData) < 55+credLen {
		return attestationAuthData{}, errors.New("attestation credential id truncated")
	}
	out.credentialID = authData[55 : 55+credLen]
	return out, nil
}

func aaguidEnvironment(aaguid []byte) (string, error) {
	switch {
	case bytes.Equal(aaguid, productionAAGUID):
		return "production", nil
	case bytes.Equal(aaguid, developmentAAGUID), bytes.Equal(aaguid, sandboxAAGUID):
		return "development", nil
	default:
		return "", errors.New("unknown App Attest AAGUID")
	}
}

func verifyCertificateNonce(cert *x509.Certificate, nonce []byte) error {
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(appAttestNonceOID) {
			continue
		}
		var outer asn1.RawValue
		if _, err := asn1.Unmarshal(ext.Value, &outer); err != nil {
			return fmt.Errorf("decode nonce extension outer value: %w", err)
		}
		var inner asn1.RawValue
		if _, err := asn1.Unmarshal(outer.Bytes, &inner); err != nil {
			return fmt.Errorf("decode nonce extension inner value: %w", err)
		}
		if inner.Tag != 4 {
			return errors.New("nonce extension is not an octet string")
		}
		if !bytes.Equal(inner.Bytes, nonce) {
			return errors.New("attestation nonce mismatch")
		}
		return nil
	}
	return errors.New("attestation nonce extension missing")
}

type ecdsaSignature struct {
	R *big.Int
	S *big.Int
}
