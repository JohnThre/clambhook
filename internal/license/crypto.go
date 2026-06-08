package license

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
)

func randomBase64URL(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func keyedHash(secret []byte, value string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func signGrant(secret []byte, grant LicenseGrant) (string, error) {
	if len(secret) < 32 {
		return "", errors.New("grant signing secret must be at least 32 bytes")
	}
	unsigned := grant
	unsigned.Signature = ""
	data, err := json.Marshal(unsigned)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func verifyGrantSignature(secret []byte, grant LicenseGrant) bool {
	got, err := signGrant(secret, grant)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(got), []byte(grant.Signature))
}

func sha256Bytes(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}
