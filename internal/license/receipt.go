package license

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type NoopReceiptRiskAssessor struct{}

func (NoopReceiptRiskAssessor) Assess(receipt []byte) (ReceiptAssessment, error) {
	return ReceiptAssessment{Receipt: receipt}, nil
}

type AppleReceiptRiskAssessor struct {
	HTTPClient *http.Client
	Endpoint   string
	TeamID     string
	KeyID      string
	privateKey *ecdsa.PrivateKey
	Now        func() time.Time
}

type AppleReceiptRiskConfig struct {
	Endpoint      string
	TeamID        string
	KeyID         string
	PrivateKeyPEM []byte
	HTTPClient    *http.Client
	Now           func() time.Time
}

func NewAppleReceiptRiskAssessor(cfg AppleReceiptRiskConfig) (*AppleReceiptRiskAssessor, error) {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://data.appattest.apple.com/v1/attestationData"
	}
	if cfg.TeamID == "" || cfg.KeyID == "" {
		return nil, errors.New("DeviceCheck team id and key id are required")
	}
	key, err := parseECDSAPrivateKey(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &AppleReceiptRiskAssessor{
		HTTPClient: client,
		Endpoint:   cfg.Endpoint,
		TeamID:     cfg.TeamID,
		KeyID:      cfg.KeyID,
		privateKey: key,
		Now:        cfg.Now,
	}, nil
}

func (a *AppleReceiptRiskAssessor) Assess(receipt []byte) (ReceiptAssessment, error) {
	if len(receipt) == 0 {
		return ReceiptAssessment{}, errors.New("App Attest receipt is empty")
	}
	token, err := a.jwt()
	if err != nil {
		return ReceiptAssessment{}, err
	}
	req, err := http.NewRequest(http.MethodPost, a.Endpoint, bytes.NewReader([]byte(base64.StdEncoding.EncodeToString(receipt))))
	if err != nil {
		return ReceiptAssessment{}, err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return ReceiptAssessment{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return ReceiptAssessment{}, err
	}
	if resp.StatusCode == http.StatusNotModified {
		return ReceiptAssessment{Receipt: receipt}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ReceiptAssessment{}, fmt.Errorf("App Attest receipt refresh failed: %s", resp.Status)
	}
	refreshed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
	if err != nil {
		return ReceiptAssessment{}, errors.New("App Attest receipt refresh response was not base64")
	}
	metric, _ := parseAppAttestRiskMetric(refreshed)
	return ReceiptAssessment{Receipt: refreshed, Metric: metric}, nil
}

func (a *AppleReceiptRiskAssessor) jwt() (string, error) {
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	header := map[string]string{
		"alg": "ES256",
		"kid": a.KeyID,
	}
	claims := map[string]any{
		"iss": a.TeamID,
		"iat": now.Unix(),
	}
	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, a.privateKey, digest[:])
	if err != nil {
		return "", err
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseECDSAPrivateKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("private key PEM is invalid")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		ecdsaKey, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("private key is not ECDSA")
		}
		return ecdsaKey, nil
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse ECDSA private key: %w", err)
	}
	return key, nil
}

func parseAppAttestRiskMetric(receipt []byte) (*int, error) {
	// App Attest receipts are CMS containers with receipt attributes inside.
	// Field 17 is encoded as: SEQUENCE { INTEGER 17, INTEGER version, OCTET STRING <ASN.1 string> }.
	for i := 0; i+8 < len(receipt); i++ {
		if receipt[i] != 0x02 || receipt[i+1] != 0x01 || receipt[i+2] != 0x11 {
			continue
		}
		j := i + 3
		if j+2 >= len(receipt) || receipt[j] != 0x02 {
			continue
		}
		versionLen := int(receipt[j+1])
		j += 2 + versionLen
		if j+2 >= len(receipt) || receipt[j] != 0x04 {
			continue
		}
		valueLen, valueStart, ok := derLength(receipt, j+1)
		if !ok || valueStart+valueLen > len(receipt) {
			continue
		}
		value := receipt[valueStart : valueStart+valueLen]
		metric, err := parseASN1StringInt(value)
		if err == nil {
			return &metric, nil
		}
	}
	return nil, errors.New("App Attest receipt risk metric not found")
}

func parseASN1StringInt(data []byte) (int, error) {
	var s string
	if _, err := asn1.Unmarshal(data, &s); err == nil {
		var n int
		if _, scanErr := fmt.Sscanf(s, "%d", &n); scanErr == nil {
			return n, nil
		}
	}
	var raw asn1.RawValue
	if _, err := asn1.Unmarshal(data, &raw); err != nil {
		return 0, err
	}
	var n int
	if _, err := fmt.Sscanf(string(raw.Bytes), "%d", &n); err != nil {
		return 0, err
	}
	return n, nil
}

func derLength(data []byte, pos int) (length int, start int, ok bool) {
	if pos >= len(data) {
		return 0, 0, false
	}
	first := data[pos]
	if first < 0x80 {
		return int(first), pos + 1, true
	}
	count := int(first & 0x7f)
	if count == 0 || count > 4 || pos+1+count > len(data) {
		return 0, 0, false
	}
	n := 0
	for i := 0; i < count; i++ {
		n = (n << 8) | int(data[pos+1+i])
	}
	return n, pos + 1 + count, true
}
