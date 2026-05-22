package v2ray

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

// ATYPDomain here is 0x02, not 0x03 (which is IPv6 in V2Ray). This guards
// against an accidental reuse of the internal/socks codec — its ATYPDomain
// is 0x03, which would render as an IPv6 frame here and corrupt the stream.
func TestEncodeAddrDomainUsesV2RayATYP(t *testing.T) {
	got, err := EncodeAddr("example.com:80")
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 0x02 {
		t.Fatalf("ATYP for domain = %#x, want 0x02 (V2Ray); 0x03 would be IPv6", got[0])
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

func TestReadAddrRoundTrip(t *testing.T) {
	cases := []struct {
		addr     string
		wantHost string
		wantPort uint16
	}{
		{"1.2.3.4:80", "1.2.3.4", 80},
		{"[2001:db8::1]:443", "2001:db8::1", 443},
		{"example.com:8080", "example.com", 8080},
	}
	for _, c := range cases {
		encoded, err := EncodeAddr(c.addr)
		if err != nil {
			t.Fatalf("encode %q: %v", c.addr, err)
		}
		host, port, err := ReadAddr(bytes.NewReader(encoded))
		if err != nil {
			t.Fatalf("read %q: %v", c.addr, err)
		}
		if host != c.wantHost || port != c.wantPort {
			t.Errorf("round-trip %q: got %s:%d, want %s:%d",
				c.addr, host, port, c.wantHost, c.wantPort)
		}
	}
}

func TestReadAddrRejectsUnknownATYP(t *testing.T) {
	// SOCKS5 domain ATYP (0x03) would be IPv6 here — reader should fail
	// cleanly rather than misparse 16 bytes of domain chars as an IPv6
	// address. In practice ReadAddr would parse it as IPv6 (legal byte
	// count), but an unknown value (e.g. 0x07) must be rejected.
	bogus := []byte{0x07, 1, 2, 3, 4, 0, 80}
	if _, _, err := ReadAddr(bytes.NewReader(bogus)); err == nil {
		t.Error("expected error for unknown ATYP 0x07")
	}
}

func TestReadAddrRejectsShortInput(t *testing.T) {
	cases := [][]byte{
		{},                        // no ATYP
		{ATYPIPv4, 1, 2, 3},       // truncated IPv4 before port
		{ATYPDomain, 5, 'a', 'b'}, // short domain
		{ATYPDomain, 0, 1, 2},     // empty domain
	}
	for i, c := range cases {
		if _, _, err := ReadAddr(bytes.NewReader(c)); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}
