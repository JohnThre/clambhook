package shadowsocks

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex: %v", err)
	}
	return b
}

// TestHKDFSHA1RFC5869 verifies our HKDF-SHA1 against RFC 5869 Test Case 4
// (the SHA-1 vector). This guards both the extract step (HMAC-SHA1 of IKM
// under the salt) and the expand loop (multi-block T(N) chaining).
func TestHKDFSHA1RFC5869(t *testing.T) {
	ikm := mustHex(t, "0b0b0b0b0b0b0b0b0b0b0b")
	salt := mustHex(t, "000102030405060708090a0b0c")
	info := mustHex(t, "f0f1f2f3f4f5f6f7f8f9")
	wantOKM := mustHex(t,
		"085a01ea1b10f36933068b56efa5ad81"+
			"a4f14b822f5b091568a9cdd4f155fda2"+
			"c22e422478d305f3f896")

	got := hkdfSHA1(ikm, salt, info, 42)
	if !bytes.Equal(got, wantOKM) {
		t.Fatalf("HKDF-SHA1 mismatch\n  got  %x\n  want %x", got, wantOKM)
	}
}

// TestHKDFSHA1Lengths confirms the function returns exactly the requested
// length and tolerates lengths smaller than one HMAC-SHA1 block (20 bytes).
func TestHKDFSHA1Lengths(t *testing.T) {
	for _, n := range []int{1, 16, 20, 21, 32, 100} {
		out := hkdfSHA1([]byte("k"), []byte("s"), []byte("i"), n)
		if len(out) != n {
			t.Errorf("len(out)=%d want %d", len(out), n)
		}
	}
}

// TestEVPBytesToKeyShort: keyLen <= 16 is just the first MD5 block.
// MD5("foo") = acbd18db4cc2f85cedef654fccc4a4d8 (canonical, easy to verify).
func TestEVPBytesToKeyShort(t *testing.T) {
	want := mustHex(t, "acbd18db4cc2f85cedef654fccc4a4d8")
	got := evpBytesToKey([]byte("foo"), 16)
	if !bytes.Equal(got, want) {
		t.Errorf("16-byte key mismatch\n  got  %x\n  want %x", got, want)
	}
}

// TestEVPBytesToKeyIterates: keyLen > 16 forces a second MD5 round chained on
// the previous digest. Compute the expected value with stdlib MD5 to verify
// the iteration math (d_2 = MD5(d_1 || password)) without hardcoding.
func TestEVPBytesToKeyIterates(t *testing.T) {
	pw := []byte("hunter2")
	d1 := md5.Sum(pw)
	h := md5.New()
	h.Write(d1[:])
	h.Write(pw)
	d2 := h.Sum(nil)
	want := append(d1[:], d2...)

	got := evpBytesToKey(pw, 32)
	if !bytes.Equal(got, want) {
		t.Errorf("32-byte key mismatch\n  got  %x\n  want %x", got, want)
	}
}

// TestEVPBytesToKeyTruncates: keyLen of 20 should give first 16 from d_1
// plus first 4 from d_2.
func TestEVPBytesToKeyTruncates(t *testing.T) {
	got := evpBytesToKey([]byte("x"), 20)
	if len(got) != 20 {
		t.Fatalf("len=%d want 20", len(got))
	}
}
