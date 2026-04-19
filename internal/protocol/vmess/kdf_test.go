package vmess

import (
	"bytes"
	"testing"
)

// KDF determinism: same inputs must produce the same output, and differing
// inputs must produce different outputs. The test doesn't pin a specific
// numeric vector (we'd need to cross-reference v2fly-core to be sure), but
// it does pin stability within our own implementation.
func TestKDFDeterministic(t *testing.T) {
	key := []byte("0123456789abcdef")
	a := KDF(key, []byte("label1"), []byte("label2"))
	b := KDF(key, []byte("label1"), []byte("label2"))
	if !bytes.Equal(a, b) {
		t.Fatalf("KDF is non-deterministic:\n a=%x\n b=%x", a, b)
	}
	if len(a) != 32 {
		t.Fatalf("KDF output length = %d, want 32", len(a))
	}
}

func TestKDFDifferentLabelsDiffer(t *testing.T) {
	key := []byte("0123456789abcdef")
	a := KDF(key, []byte("label1"))
	b := KDF(key, []byte("label2"))
	if bytes.Equal(a, b) {
		t.Fatal("KDF produced identical output for different labels")
	}
}

func TestKDFDifferentKeysDiffer(t *testing.T) {
	a := KDF([]byte("key-alpha"), []byte("label"))
	b := KDF([]byte("key-beta"), []byte("label"))
	if bytes.Equal(a, b) {
		t.Fatal("KDF produced identical output for different keys")
	}
}

// TestKDFChainDependsOnOrder verifies that path order matters — swapping
// two labels produces a different output. This is what we want: the KDF
// treats its paths as a sequence, not a set.
func TestKDFChainDependsOnOrder(t *testing.T) {
	key := []byte("k")
	ab := KDF(key, []byte("a"), []byte("b"))
	ba := KDF(key, []byte("b"), []byte("a"))
	if bytes.Equal(ab, ba) {
		t.Fatal("KDF is order-insensitive; it should treat paths as a sequence")
	}
}
