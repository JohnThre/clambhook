package trojan

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/clambhook/clambhook/internal/protocol"
	"github.com/clambhook/clambhook/pkg/cnet"
)

// Verifies cnet.SHA224 matches the canonical RFC 3874 vector. Guards both the
// C implementation and the cgo bridge.
func TestSHA224RFC3874Vector(t *testing.T) {
	got := cnet.SHA224([]byte("abc"))
	want, _ := hex.DecodeString("23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7")
	if !bytes.Equal(got, want) {
		t.Fatalf("SHA-224(\"abc\") mismatch\n got=%x\nwant=%x", got, want)
	}
}

func TestEncodeAddrIPv4(t *testing.T) {
	got, err := encodeAddr("1.2.3.4:80")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{atypIPv4, 1, 2, 3, 4, 0x00, 0x50}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestEncodeAddrIPv6(t *testing.T) {
	got, err := encodeAddr("[2001:db8::1]:443")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		atypIPv6,
		0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01,
		0x01, 0xbb,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestEncodeAddrDomain(t *testing.T) {
	got, err := encodeAddr("example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		atypDomain, 11,
		'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm',
		0x01, 0xbb,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestEncodeAddrRejectsBadInput(t *testing.T) {
	cases := []string{
		"",                    // empty
		"example.com",         // no port
		"example.com:99999",   // port out of range
		"example.com:notanum", // non-numeric port
	}
	for _, c := range cases {
		if _, err := encodeAddr(c); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestEncodeAddrRejectsLongDomain(t *testing.T) {
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := encodeAddr(string(long) + ":443"); err == nil {
		t.Error("expected error for domain > 255 bytes")
	}
}

func TestEncodeHeaderFullBytes(t *testing.T) {
	var hashHex [56]byte
	sum := cnet.SHA224([]byte("secret"))
	hex.Encode(hashHex[:], sum)

	got, err := encodeHeader(hashHex, cmdConnect, "1.2.3.4:80")
	if err != nil {
		t.Fatal(err)
	}

	want := make([]byte, 0, 56+2+8+2)
	want = append(want, hashHex[:]...)
	want = append(want, '\r', '\n')
	want = append(want, cmdConnect, atypIPv4, 1, 2, 3, 4, 0x00, 0x50)
	want = append(want, '\r', '\n')

	if !bytes.Equal(got, want) {
		t.Fatalf("header mismatch\n got=%x\nwant=%x", got, want)
	}
}

func TestEncodeHeaderUDPAssociate(t *testing.T) {
	var hashHex [56]byte
	sum := cnet.SHA224([]byte("secret"))
	hex.Encode(hashHex[:], sum)

	got, err := encodeHeader(hashHex, cmdUDPAssociate, "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	// Bytes at offset 58 should be CMD=0x03, then atypIPv4, then 0.0.0.0:0.
	if got[58] != cmdUDPAssociate {
		t.Errorf("cmd byte = %#x, want %#x", got[58], cmdUDPAssociate)
	}
	if got[59] != atypIPv4 {
		t.Errorf("atyp = %#x, want %#x", got[59], atypIPv4)
	}
}

func TestParseConfigMissingPassword(t *testing.T) {
	_, err := parseConfig(protocol.Server{
		Address:  "example.com:443",
		Settings: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing password")
	}
}

func TestParseConfigSNIDefaultsToHost(t *testing.T) {
	cfg, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"password": "hunter2",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.sni != "example.com" {
		t.Fatalf("sni = %q, want %q", cfg.sni, "example.com")
	}
}

func TestParseConfigSNIExplicit(t *testing.T) {
	cfg, err := parseConfig(protocol.Server{
		Address: "203.0.113.5:443",
		Settings: map[string]any{
			"password": "hunter2",
			"sni":      "cloud.example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.sni != "cloud.example.com" {
		t.Fatalf("sni = %q, want %q", cfg.sni, "cloud.example.com")
	}
}

func TestParseConfigPrecomputesHashHex(t *testing.T) {
	cfg, err := parseConfig(protocol.Server{
		Address: "example.com:443",
		Settings: map[string]any{
			"password": "hunter2",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var want [56]byte
	hex.Encode(want[:], cnet.SHA224([]byte("hunter2")))
	if cfg.passwordHashHex != want {
		t.Fatalf("hash hex = %s, want %s", cfg.passwordHashHex, want)
	}
}
