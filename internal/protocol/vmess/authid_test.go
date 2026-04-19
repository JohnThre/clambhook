package vmess

import (
	"bytes"
	"crypto/aes"
	"encoding/binary"
	"hash/crc32"
	"testing"
	"time"
)

// TestAuthIDRoundTrip recovers the plaintext of an AuthID by inverting the
// AES-128-ECB encryption with the same KDF-derived key, then verifies the
// CRC32 checksum and timestamp. This is exactly what the server does — so
// if this test passes, a real server would accept our AuthIDs too.
func TestAuthIDRoundTrip(t *testing.T) {
	var cmdKey [16]byte
	for i := range cmdKey {
		cmdKey[i] = byte(i) + 1
	}

	now := time.Unix(1_700_000_000, 0)
	authID, err := generateAuthID(cmdKey, now)
	if err != nil {
		t.Fatal(err)
	}

	key := KDF(cmdKey[:], kdfLabelAuthIDEncKey)[:16]
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	var plain [16]byte
	block.Decrypt(plain[:], authID[:])

	ts := int64(binary.BigEndian.Uint64(plain[0:8]))
	if ts != now.Unix() {
		t.Errorf("decoded timestamp = %d, want %d", ts, now.Unix())
	}

	wantSum := crc32.ChecksumIEEE(plain[0:12])
	gotSum := binary.BigEndian.Uint32(plain[12:16])
	if wantSum != gotSum {
		t.Errorf("CRC32 = %#x, want %#x", gotSum, wantSum)
	}
}

// TestAuthIDRandomized — two AuthIDs generated back-to-back must differ,
// otherwise the replay-window check at the server becomes a single-shot
// nonce that attackers can easily spot.
func TestAuthIDRandomized(t *testing.T) {
	var cmdKey [16]byte
	a, err := generateAuthID(cmdKey, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	b, err := generateAuthID(cmdKey, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a[:], b[:]) {
		t.Fatal("two AuthIDs at the same timestamp are identical — the random field isn't doing its job")
	}
}
