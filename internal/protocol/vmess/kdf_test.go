// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package vmess

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"hash"
	"testing"
)

// TestKDFMatchesReference checks the nested-HMAC construction against an
// independent implementation of the same algorithm.
func TestKDFMatchesReference(t *testing.T) {
	key := []byte("some-command-key")
	path := [][]byte{[]byte("label-a"), []byte("label-b")}

	got := kdf(key, path...)
	want := referenceKDF(key, path...)

	if !bytes.Equal(got, want) {
		t.Fatalf("kdf = %x, want %x", got, want)
	}
	if len(got) != 32 {
		t.Fatalf("kdf len = %d, want 32", len(got))
	}
}

func TestKDF16Length(t *testing.T) {
	if got := kdf16([]byte("k"), []byte("p")); len(got) != 16 {
		t.Fatalf("kdf16 len = %d, want 16", len(got))
	}
}

func TestKDFNoPath(t *testing.T) {
	got := kdf([]byte("k"))
	mac := hmac.New(sha256.New, kdfSaltConst)
	mac.Write([]byte("k"))
	if !bytes.Equal(got, mac.Sum(nil)) {
		t.Fatal("kdf with no path should equal HMAC(salt, key)")
	}
}

// referenceKDF is an independent closure-based implementation of the VMESS KDF,
// used only to cross-check kdf(). Each path element wraps the previous layer's
// constructor as the hash for the next HMAC.
func referenceKDF(key []byte, path ...[]byte) []byte {
	ctor := func() hash.Hash { return hmac.New(sha256.New, kdfSaltConst) }
	for _, p := range path {
		parent := ctor
		label := p
		ctor = func() hash.Hash { return hmac.New(parent, label) }
	}
	m := ctor()
	m.Write(key)
	return m.Sum(nil)
}
