package socks

import (
	"bytes"
	"testing"
)

func TestEncodeAddrIPv4(t *testing.T) {
	got, err := EncodeAddr("1.2.3.4:80")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{ATYPIPv4, 1, 2, 3, 4, 0x00, 0x50}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestEncodeAddrIPv6(t *testing.T) {
	got, err := EncodeAddr("[2001:db8::1]:443")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		ATYPIPv6,
		0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01,
		0x01, 0xbb,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestEncodeAddrDomain(t *testing.T) {
	got, err := EncodeAddr("example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		ATYPDomain, 11,
		'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm',
		0x01, 0xbb,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestEncodeAddrRejectsBadInput(t *testing.T) {
	cases := []string{
		"",
		"example.com",
		"example.com:99999",
		"example.com:notanum",
	}
	for _, c := range cases {
		if _, err := EncodeAddr(c); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestEncodeAddrRejectsLongDomain(t *testing.T) {
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := EncodeAddr(string(long) + ":443"); err == nil {
		t.Error("expected error for domain > 255 bytes")
	}
}

// TestRoundTrip verifies that bytes produced by EncodeAddr can be parsed
// back to the same host:port via ReadAddr. This guards both halves of the
// codec against silent drift.
func TestRoundTrip(t *testing.T) {
	cases := []struct {
		addr     string
		wantHost string
		wantPort uint16
	}{
		{"1.2.3.4:80", "1.2.3.4", 80},
		{"[2001:db8::1]:443", "2001:db8::1", 443},
		{"example.com:8443", "example.com", 8443},
	}
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			enc, err := EncodeAddr(tc.addr)
			if err != nil {
				t.Fatal(err)
			}
			host, port, err := ReadAddr(bytes.NewReader(enc))
			if err != nil {
				t.Fatal(err)
			}
			if host != tc.wantHost || port != tc.wantPort {
				t.Errorf("got %s:%d, want %s:%d", host, port, tc.wantHost, tc.wantPort)
			}
		})
	}
}

func TestReadAddrRejectsEmptyDomain(t *testing.T) {
	// ATYPDomain with length=0 must be rejected.
	bad := []byte{ATYPDomain, 0x00, 0x00, 0x50}
	if _, _, err := ReadAddr(bytes.NewReader(bad)); err == nil {
		t.Error("expected error for empty domain")
	}
}

func TestReadAddrRejectsUnknownATYP(t *testing.T) {
	bad := []byte{0x99, 1, 2, 3, 4}
	if _, _, err := ReadAddr(bytes.NewReader(bad)); err == nil {
		t.Error("expected error for unknown atyp")
	}
}
