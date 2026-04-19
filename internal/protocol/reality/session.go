package reality

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
)

// Xray version triplet embedded into the Reality session_id plaintext.
// These mirror core.Version_x / Version_y / Version_z in xray-core. They
// drift with xray releases — a mismatch doesn't break the handshake (the
// server only uses the ciphertext + tag, not the plaintext bytes
// directly), but it's a soft signal to server-side logging and a useful
// knob if a future Reality server starts version-gating.
const (
	versionX byte = 26
	versionY byte = 4
	versionZ byte = 17
)

// buildSessionIDPlaintext lays out the 16-byte plaintext that gets
// encrypted into the TLS session_id field. Layout:
//
//	[0]    versionX
//	[1]    versionY
//	[2]    versionZ
//	[3]    reserved (0)
//	[4:8]  unix timestamp, big-endian uint32
//	[8:16] short_id, zero-padded right to 8 bytes
//
// The remaining 16 bytes of the 32-byte session_id field are the GCM
// tag slot, filled by sealSessionID.
func buildSessionIDPlaintext(ts uint32, shortID [8]byte) [16]byte {
	var p [16]byte
	p[0] = versionX
	p[1] = versionY
	p[2] = versionZ
	p[3] = 0
	binary.BigEndian.PutUint32(p[4:8], ts)
	copy(p[8:], shortID[:])
	return p
}

// sealSessionID encrypts the 16-byte plaintext into the 32-byte on-wire
// session_id value. Returns a 32-byte array (16 B ciphertext ‖ 16 B tag).
//
// The caller is responsible for the crucial detail: aad must be the
// serialized ClientHello with the 32-byte session_id slot (offset 39..71)
// zeroed out. Both client and server compute AAD this way; passing the
// already-encrypted ciphertext in the AAD slot would break decryption on
// the server side.
func sealSessionID(authKey, nonce []byte, plaintext [16]byte, aad []byte) ([32]byte, error) {
	var out [32]byte
	if len(authKey) != 32 {
		return out, fmt.Errorf("reality: authKey len: want 32, got %d", len(authKey))
	}
	if len(nonce) != 12 {
		return out, fmt.Errorf("reality: nonce len: want 12, got %d", len(nonce))
	}
	block, err := aes.NewCipher(authKey)
	if err != nil {
		return out, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return out, err
	}
	// dst == plaintext[:0] pattern is explicitly permitted by cipher.AEAD.
	// We build on a 16-byte plaintext copy so the caller's buffer stays
	// untouched — tests rely on this to assert plaintext invariance.
	buf := make([]byte, 16, 32)
	copy(buf, plaintext[:])
	sealed := gcm.Seal(buf[:0], nonce, buf, aad)
	if len(sealed) != 32 {
		return out, fmt.Errorf("reality: sealed len: want 32, got %d", len(sealed))
	}
	copy(out[:], sealed)
	return out, nil
}
