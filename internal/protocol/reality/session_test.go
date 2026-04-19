package reality

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestBuildSessionIDPlaintext_Layout(t *testing.T) {
	sid := [8]byte{0xa1, 0xb2, 0xc3, 0xd4}
	p := buildSessionIDPlaintext(0x12345678, sid)

	if p[0] != versionX || p[1] != versionY || p[2] != versionZ {
		t.Errorf("version bytes: got %x %x %x", p[0], p[1], p[2])
	}
	if p[3] != 0 {
		t.Errorf("reserved byte: got %x", p[3])
	}
	if ts := binary.BigEndian.Uint32(p[4:8]); ts != 0x12345678 {
		t.Errorf("timestamp: got %x", ts)
	}
	if !bytes.Equal(p[8:], sid[:]) {
		t.Errorf("short_id bytes: got %x want %x", p[8:], sid)
	}
}

func TestSealSessionID_RoundTripAgainstGCM(t *testing.T) {
	key, _ := hex.DecodeString("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	nonce, _ := hex.DecodeString("0102030405060708090a0b0c")
	aad := []byte("arbitrary aad bytes for AEAD")
	plaintext := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}

	out, err := sealSessionID(key, nonce, plaintext, aad)
	if err != nil {
		t.Fatalf("sealSessionID: %v", err)
	}

	// Decrypt with a fresh cipher.GCM and confirm we get the same plaintext back.
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	dec, err := gcm.Open(nil, nonce, out[:], aad)
	if err != nil {
		t.Fatalf("gcm.Open: %v", err)
	}
	if !bytes.Equal(dec, plaintext[:]) {
		t.Errorf("roundtrip mismatch: got %x want %x", dec, plaintext)
	}
}

func TestSealSessionID_TagDetectsAADTamper(t *testing.T) {
	key, _ := hex.DecodeString("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	nonce := make([]byte, 12)
	aad := []byte("original aad")
	plaintext := [16]byte{1, 2, 3, 4}

	out, err := sealSessionID(key, nonce, plaintext, aad)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	if _, err := gcm.Open(nil, nonce, out[:], []byte("tampered aad")); err == nil {
		t.Error("expected Open to fail on tampered AAD")
	}
}

func TestSealSessionID_RejectsBadLengths(t *testing.T) {
	shortKey := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := sealSessionID(shortKey, nonce, [16]byte{}, nil); err == nil {
		t.Error("want error on 16-byte key")
	}
	goodKey := make([]byte, 32)
	shortNonce := make([]byte, 8)
	if _, err := sealSessionID(goodKey, shortNonce, [16]byte{}, nil); err == nil {
		t.Error("want error on 8-byte nonce")
	}
}
