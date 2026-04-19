package reality

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"strings"
	"testing"
)

func TestVerifyServerCert_ValidTag(t *testing.T) {
	authKey := make([]byte, 32)
	if _, err := rand.Read(authKey); err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	mac := hmac.New(sha512.New, authKey)
	mac.Write(pub)
	tag := mac.Sum(nil)

	cert := &x509.Certificate{
		PublicKey: pub,
		Signature: tag,
	}
	if err := verifyServerCert(authKey, cert); err != nil {
		t.Errorf("verify: %v", err)
	}
}

func TestVerifyServerCert_TamperedTag(t *testing.T) {
	authKey := make([]byte, 32)
	rand.Read(authKey)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)

	mac := hmac.New(sha512.New, authKey)
	mac.Write(pub)
	tag := mac.Sum(nil)
	tag[0] ^= 0x01

	cert := &x509.Certificate{PublicKey: pub, Signature: tag}
	err := verifyServerCert(authKey, cert)
	if err == nil || !strings.Contains(err.Error(), "auth failed") {
		t.Errorf("want auth-failed error, got %v", err)
	}
}

func TestVerifyServerCert_NonEd25519(t *testing.T) {
	authKey := make([]byte, 32)
	rand.Read(authKey)
	// ECDSA P-256 pub key — must be rejected.
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cert := &x509.Certificate{PublicKey: &priv.PublicKey, Signature: []byte("whatever")}
	err := verifyServerCert(authKey, cert)
	if err == nil || !strings.Contains(err.Error(), "non-ed25519") {
		t.Errorf("want non-ed25519 error, got %v", err)
	}
}

func TestVerifyServerCert_NilCert(t *testing.T) {
	if err := verifyServerCert(make([]byte, 32), nil); err == nil {
		t.Error("want error for nil cert")
	}
}
