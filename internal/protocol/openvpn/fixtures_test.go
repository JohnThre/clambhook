package openvpn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"sync"
	"testing"
	"time"
)

// testFixturePEMs returns a (caPEM, clientCertPEM, clientKeyPEM) triple
// suitable for exercising parseConfig. Generated lazily once per test
// binary — generation is ~50ms of ECDSA keygen, which is cheap but
// unnecessary to repeat for every TestParseConfig* case.
//
// These are self-signed and not trusted by any real CA; parseConfig
// only validates PEM structure + x509 parseability, not trust chains,
// so this is sufficient.
var (
	fixturesOnce sync.Once
	fixCAPEM     string
	fixCertPEM   string
	fixKeyPEM    string
	fixErr       error
)

func testFixturePEMs(t *testing.T) (ca, cert, key string) {
	t.Helper()
	fixturesOnce.Do(generateFixtures)
	if fixErr != nil {
		t.Fatalf("fixture generation: %v", fixErr)
	}
	return fixCAPEM, fixCertPEM, fixKeyPEM
}

func generateFixtures() {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fixErr = err
		return
	}
	caTpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "clambhook-test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:         true,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		fixErr = err
		return
	}

	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fixErr = err
		return
	}
	clientTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "clambhook-test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTpl, caTpl, &clientKey.PublicKey, caKey)
	if err != nil {
		fixErr = err
		return
	}
	clientKeyDER, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		fixErr = err
		return
	}

	fixCAPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	fixCertPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}))
	fixKeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: clientKeyDER}))
}
