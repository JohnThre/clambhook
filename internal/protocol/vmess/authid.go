package vmess

import (
	"crypto/aes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"time"
)

// generateAuthID produces the 16-byte client Auth ID block that opens a
// VMess AEAD request. Layout before encryption:
//
//	[ time(8 BE, seconds since epoch) | random(4) | crc32(time||random) (4 BE) ]
//
// The block is then encrypted with a single AES-128-ECB application under a
// key derived from cmdKey via KDF(kdfLabelAuthIDEncKey). The resulting
// ciphertext is the AuthID that the server unwraps to check time skew
// (±120s default) and prove UUID ownership.
func generateAuthID(cmdKey [16]byte, now time.Time) ([16]byte, error) {
	var out [16]byte
	var plain [16]byte

	binary.BigEndian.PutUint64(plain[0:8], uint64(now.Unix()))
	if _, err := rand.Read(plain[8:12]); err != nil {
		return out, fmt.Errorf("vmess: authid random: %w", err)
	}
	sum := crc32.ChecksumIEEE(plain[0:12])
	binary.BigEndian.PutUint32(plain[12:16], sum)

	key := KDF(cmdKey[:], kdfLabelAuthIDEncKey)[:16]
	block, err := aes.NewCipher(key)
	if err != nil {
		return out, fmt.Errorf("vmess: authid cipher: %w", err)
	}
	// Single-block ECB — the plaintext is exactly 16 bytes by construction.
	block.Encrypt(out[:], plain[:])
	return out, nil
}
