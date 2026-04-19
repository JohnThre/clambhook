package vmess

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"testing"
)

func makeAEAD(t *testing.T) cipher.AEAD {
	t.Helper()
	key := make([]byte, 16)
	for i := range key {
		key[i] = byte(i)
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	a, err := cipher.NewGCM(b)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// Round-trip a single short chunk through seal/open using matched codec
// instances (writer and reader share the same IV). Exercises the length
// mask, counter, and AEAD together.
func TestChunkRoundTripShort(t *testing.T) {
	var iv [16]byte
	for i := range iv {
		iv[i] = byte(i + 1)
	}
	wCodec := newChunkCodec(makeAEAD(t), iv)
	rCodec := newChunkCodec(makeAEAD(t), iv)

	payload := []byte("hello vmess")
	var buf bytes.Buffer
	if _, err := wCodec.seal(&buf, payload); err != nil {
		t.Fatal(err)
	}
	got, err := rCodec.open(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("got %q, want %q", got, payload)
	}
}

// Multiple chunks in a row must stay in sync: the counter increments per
// chunk and the Shake128 mask advances by exactly 2 bytes per chunk. If
// either side miscounts, the length or AEAD check fails.
func TestChunkRoundTripMultiple(t *testing.T) {
	var iv [16]byte
	wCodec := newChunkCodec(makeAEAD(t), iv)
	rCodec := newChunkCodec(makeAEAD(t), iv)

	var buf bytes.Buffer
	payloads := [][]byte{
		[]byte("first"),
		[]byte("second chunk is longer"),
		bytes.Repeat([]byte{0xaa}, 1024),
	}
	for _, p := range payloads {
		if _, err := wCodec.seal(&buf, p); err != nil {
			t.Fatalf("seal: %v", err)
		}
	}
	for i, want := range payloads {
		got, err := rCodec.open(&buf)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("chunk %d mismatch", i)
		}
	}
}

// An IV difference between writer and reader must cause the length mask to
// diverge, producing a garbage length that the reader rejects.
func TestChunkIVDivergence(t *testing.T) {
	var ivA, ivB [16]byte
	for i := range ivA {
		ivA[i] = byte(i)
		ivB[i] = byte(i) + 1
	}
	wCodec := newChunkCodec(makeAEAD(t), ivA)
	rCodec := newChunkCodec(makeAEAD(t), ivB)

	var buf bytes.Buffer
	if _, err := wCodec.seal(&buf, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := rCodec.open(&buf); err == nil {
		t.Error("expected decrypt failure with mismatched IVs")
	}
}
