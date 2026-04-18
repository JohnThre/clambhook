package shadowsocks

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"
)

func TestNonceIncrementZero(t *testing.T) {
	var n nonce
	n.increment()
	want := nonce{1}
	if n != want {
		t.Errorf("got %v want %v", n, want)
	}
}

// TestNonceIncrementCarry exercises the little-endian carry across a byte
// boundary — the classic off-by-one spot.
func TestNonceIncrementCarry(t *testing.T) {
	n := nonce{0xff, 0x00}
	n.increment()
	want := nonce{0x00, 0x01}
	if n != want {
		t.Errorf("got %v want %v", n, want)
	}
}

// TestNonceIncrementHighOrderCarry: ff..ff (12 bytes) wraps to 00..00.
// Documents (and pins down) wrap behavior even though we'll never reach it
// in practice.
func TestNonceIncrementHighOrderCarry(t *testing.T) {
	n := nonce{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	n.increment()
	want := nonce{}
	if n != want {
		t.Errorf("got %v want %v", n, want)
	}
}

// TestStreamRoundTrip pumps various payload sizes through the writer and
// confirms the reader recovers them byte-for-byte. Sizes are chosen to
// exercise: tiny payloads, exactly-at-cap, just-over-cap (forces split),
// and many-cap (multiple splits).
func TestStreamRoundTrip(t *testing.T) {
	for _, name := range []string{"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"} {
		t.Run(name, func(t *testing.T) {
			spec, err := cipherByName(name)
			if err != nil {
				t.Skipf("cipher unavailable: %v", err)
			}
			subkey := bytes.Repeat([]byte{0xa5}, spec.keySize)

			for _, size := range []int{1, 100, maxChunkSize, maxChunkSize + 1, 100_000} {
				t.Run("size", func(t *testing.T) {
					payload := make([]byte, size)
					if _, err := rand.Read(payload); err != nil {
						t.Fatal(err)
					}

					var buf bytes.Buffer
					sw := newStreamWriter(&buf, spec, subkey)
					if _, err := sw.Write(payload); err != nil {
						t.Fatalf("write: %v", err)
					}

					sr := newStreamReader(&buf, spec, subkey)
					got := make([]byte, len(payload))
					if _, err := io.ReadFull(sr, got); err != nil {
						t.Fatalf("read: %v", err)
					}
					if !bytes.Equal(got, payload) {
						t.Errorf("size=%d: round-trip mismatch", size)
					}
				})
			}
		})
	}
}

// TestStreamShortRead: caller buffer smaller than a chunk; subsequent reads
// must drain the leftover plaintext from `pending` without re-reading the
// underlying stream (which would re-decrypt with the next nonce, failing).
func TestStreamShortRead(t *testing.T) {
	spec, err := cipherByName("chacha20-ietf-poly1305")
	if err != nil {
		t.Fatal(err)
	}
	subkey := bytes.Repeat([]byte{0x42}, spec.keySize)
	payload := bytes.Repeat([]byte{0xcc}, 1024)

	var buf bytes.Buffer
	sw := newStreamWriter(&buf, spec, subkey)
	if _, err := sw.Write(payload); err != nil {
		t.Fatal(err)
	}

	sr := newStreamReader(&buf, spec, subkey)
	got := make([]byte, 0, 1024)
	small := make([]byte, 17) // odd size to force partial drains
	for len(got) < 1024 {
		n, err := sr.Read(small)
		if err != nil {
			t.Fatalf("read after %d bytes: %v", len(got), err)
		}
		got = append(got, small[:n]...)
	}
	if !bytes.Equal(got, payload) {
		t.Error("payload mismatch")
	}
}

// TestStreamTamperPayload: flipping a bit in the encrypted payload of the
// first chunk must cause the reader to fail with an auth error rather than
// silently returning garbage.
func TestStreamTamperPayload(t *testing.T) {
	spec, err := cipherByName("chacha20-ietf-poly1305")
	if err != nil {
		t.Fatal(err)
	}
	subkey := bytes.Repeat([]byte{0x77}, spec.keySize)

	var buf bytes.Buffer
	sw := newStreamWriter(&buf, spec, subkey)
	if _, err := sw.Write([]byte("sensitive payload")); err != nil {
		t.Fatal(err)
	}

	// First chunk layout: [enc_len(2) || len_tag(16)] [enc_payload || payload_tag(16)]
	// Flip a bit deep into the payload portion.
	wire := buf.Bytes()
	wire[2+16+5] ^= 0x01

	sr := newStreamReader(bytes.NewReader(wire), spec, subkey)
	out := make([]byte, 64)
	if _, err := sr.Read(out); err == nil {
		t.Fatal("expected auth error on tampered payload")
	}
}

// TestStreamTamperLength: flipping a bit in the encrypted length frame must
// also fail. A naive impl might trust the length and read garbage.
func TestStreamTamperLength(t *testing.T) {
	spec, err := cipherByName("chacha20-ietf-poly1305")
	if err != nil {
		t.Fatal(err)
	}
	subkey := bytes.Repeat([]byte{0x88}, spec.keySize)

	var buf bytes.Buffer
	sw := newStreamWriter(&buf, spec, subkey)
	if _, err := sw.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}

	wire := buf.Bytes()
	wire[0] ^= 0x01

	sr := newStreamReader(bytes.NewReader(wire), spec, subkey)
	out := make([]byte, 64)
	if _, err := sr.Read(out); err == nil {
		t.Fatal("expected auth error on tampered length")
	}
}

// TestStreamRejectsZeroLength: a peer sending a length-0 chunk would let
// them spin our reader forever. Reject defensively.
func TestStreamRejectsZeroLength(t *testing.T) {
	spec, err := cipherByName("chacha20-ietf-poly1305")
	if err != nil {
		t.Fatal(err)
	}
	subkey := bytes.Repeat([]byte{0x33}, spec.keySize)

	// Hand-craft a chunk with length=0 (encrypted under nonce 0).
	var n nonce
	zero := []byte{0x00, 0x00}
	ct, tag, err := spec.encrypt(subkey, n[:], zero, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire := append(ct, tag...)

	sr := newStreamReader(bytes.NewReader(wire), spec, subkey)
	out := make([]byte, 64)
	if _, err := sr.Read(out); err == nil {
		t.Fatal("expected error on zero-length chunk")
	}
}
