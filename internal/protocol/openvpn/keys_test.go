package openvpn

import (
	"bytes"
	"testing"
)

func TestSplitKeyBlockShape(t *testing.T) {
	// Use a byte block where each octet equals its position, so we can
	// see the slicing at a glance.
	block := make([]byte, keyBlockSize)
	for i := range block {
		block[i] = byte(i)
	}
	km, err := splitKeyBlock(block, "AES-256-GCM")
	if err != nil {
		t.Fatal(err)
	}
	// client cipher = bytes 0..32
	want := make([]byte, 32)
	for i := range want {
		want[i] = byte(i)
	}
	if !bytes.Equal(km.clientCipherKey, want) {
		t.Errorf("clientCipherKey mismatch")
	}
	// client implicit IV = bytes 64..72
	wantIV := make([]byte, 8)
	for i := range wantIV {
		wantIV[i] = byte(64 + i)
	}
	if !bytes.Equal(km.clientImplicitIV, wantIV) {
		t.Errorf("clientImplicitIV mismatch")
	}
	// server cipher = bytes 128..160
	wantS := make([]byte, 32)
	for i := range wantS {
		wantS[i] = byte(128 + i)
	}
	if !bytes.Equal(km.serverCipherKey, wantS) {
		t.Errorf("serverCipherKey mismatch")
	}
	// server implicit IV = bytes 192..200
	wantSIV := make([]byte, 8)
	for i := range wantSIV {
		wantSIV[i] = byte(192 + i)
	}
	if !bytes.Equal(km.serverImplicitIV, wantSIV) {
		t.Errorf("serverImplicitIV mismatch")
	}
}

func TestSplitKeyBlockRejectsWrongSize(t *testing.T) {
	if _, err := splitKeyBlock(make([]byte, 100), "AES-256-GCM"); err == nil {
		t.Fatal("expected error for short block")
	}
}

func TestSplitKeyBlockRejectsUnsupportedCipher(t *testing.T) {
	if _, err := splitKeyBlock(make([]byte, keyBlockSize), "AES-128-CBC"); err == nil {
		t.Fatal("expected error for unsupported cipher")
	}
}
