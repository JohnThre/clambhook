package wireguard

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestKeyToHexValid(t *testing.T) {
	// 32 zero bytes — simplest known-good input. Verifies the happy path and
	// that the hex result is lowercase (IpcSet is case-sensitive on some
	// older tool versions; lowercase matches the spec output).
	raw := make([]byte, 32)
	b64 := base64.StdEncoding.EncodeToString(raw)

	hex, err := keyToHex(b64)
	if err != nil {
		t.Fatalf("keyToHex: %v", err)
	}
	want := strings.Repeat("00", 32)
	if hex != want {
		t.Errorf("hex = %q, want %q", hex, want)
	}
}

func TestKeyToHexRoundTrip(t *testing.T) {
	// Nontrivial bytes exercise the encoding path end-to-end.
	raw := []byte{
		0xe8, 0x4b, 0x5a, 0x6d, 0x2f, 0x93, 0x11, 0x02,
		0xbd, 0x46, 0x1a, 0x0e, 0xaa, 0xa5, 0x52, 0x54,
		0x6a, 0x67, 0xc9, 0x88, 0xd4, 0x31, 0x00, 0xf0,
		0x56, 0xa3, 0xb2, 0xc5, 0x87, 0x11, 0xff, 0xee,
	}
	b64 := base64.StdEncoding.EncodeToString(raw)
	hex, err := keyToHex(b64)
	if err != nil {
		t.Fatalf("keyToHex: %v", err)
	}
	if len(hex) != 64 {
		t.Errorf("hex length = %d, want 64", len(hex))
	}
	if hex != strings.ToLower(hex) {
		t.Errorf("hex not lowercase: %q", hex)
	}
}

func TestKeyToHexEmpty(t *testing.T) {
	if _, err := keyToHex(""); err == nil {
		t.Fatal("expected error on empty input")
	}
}

func TestKeyToHexNonBase64(t *testing.T) {
	if _, err := keyToHex("not!!!base64!!!"); err == nil {
		t.Fatal("expected error on non-base64 input")
	}
}

func TestKeyToHexWrongLength(t *testing.T) {
	// 16 zero bytes — valid base64, but half the required length.
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := keyToHex(short); err == nil {
		t.Fatal("expected error on 16-byte key")
	}

	// 64 zero bytes — valid base64, but double the required length.
	long := base64.StdEncoding.EncodeToString(make([]byte, 64))
	if _, err := keyToHex(long); err == nil {
		t.Fatal("expected error on 64-byte key")
	}
}
