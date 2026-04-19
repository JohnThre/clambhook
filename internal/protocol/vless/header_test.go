package vless

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
)

func TestEncodeRequestIPv4(t *testing.T) {
	id := uuid.MustParse("b831381d-6324-4d53-ad4f-8cda48b30811")
	got, err := encodeRequest(id, cmdTCP, "1.2.3.4:80")
	if err != nil {
		t.Fatal(err)
	}

	want := make([]byte, 0, 25)
	want = append(want, version)
	want = append(want, id[:]...)
	want = append(want, 0x00)       // addon_len
	want = append(want, cmdTCP)     // cmd
	want = append(want, 0x00, 0x50) // port 80 BE
	want = append(want, 0x01)       // ATYP IPv4
	want = append(want, 1, 2, 3, 4)

	if !bytes.Equal(got, want) {
		t.Fatalf("header mismatch\n got=%x\nwant=%x", got, want)
	}
}

func TestEncodeRequestDomain(t *testing.T) {
	id := uuid.MustParse("b831381d-6324-4d53-ad4f-8cda48b30811")
	got, err := encodeRequest(id, cmdTCP, "example.com:443")
	if err != nil {
		t.Fatal(err)
	}

	// Offsets: ver(0), uuid(1..16), addon_len(17), cmd(18), port(19..20),
	// atyp(21), addr(22..).
	if got[18] != cmdTCP {
		t.Errorf("cmd = %#x, want %#x", got[18], cmdTCP)
	}
	if got[19] != 0x01 || got[20] != 0xbb {
		t.Errorf("port = %02x%02x, want 01bb (443 BE)", got[19], got[20])
	}
	// ATYPDomain here is 0x02, not 0x03 — using the SOCKS5 domain value would
	// make VLESS servers misparse the next bytes as a truncated IPv6 address.
	if got[21] != 0x02 {
		t.Errorf("atyp = %#x, want 0x02 (V2Ray domain)", got[21])
	}
	if got[22] != 11 {
		t.Errorf("domain len = %d, want 11", got[22])
	}
	if string(got[23:34]) != "example.com" {
		t.Errorf("domain = %q, want example.com", got[23:34])
	}
}

func TestEncodeRequestUDPCmd(t *testing.T) {
	id := uuid.MustParse("b831381d-6324-4d53-ad4f-8cda48b30811")
	got, err := encodeRequest(id, cmdUDP, "1.2.3.4:53")
	if err != nil {
		t.Fatal(err)
	}
	// cmd byte is at offset 1 (ver) + 16 (uuid) + 1 (addon_len) = 18
	if got[18] != cmdUDP {
		t.Fatalf("cmd = %#x, want %#x", got[18], cmdUDP)
	}
}

func TestEncodeRequestRejectsBadPort(t *testing.T) {
	id := uuid.Nil
	if _, err := encodeRequest(id, cmdTCP, "1.2.3.4:99999"); err == nil {
		t.Error("expected error for port out of range")
	}
}

func TestReadResponseNoAddons(t *testing.T) {
	r := bytes.NewReader([]byte{0x00, 0x00, 'd', 'a', 't', 'a'})
	if err := readResponse(r); err != nil {
		t.Fatal(err)
	}
	// After readResponse, "data" should remain in the reader — verifies the
	// header parser reads *exactly* as much as the protocol defines and no
	// more, so application bytes aren't consumed.
	rest := bytes.NewBuffer(nil)
	_, _ = rest.ReadFrom(r)
	if rest.String() != "data" {
		t.Errorf("leftover = %q, want data", rest.String())
	}
}

func TestReadResponseWithAddons(t *testing.T) {
	// ver=0x00, addon_len=0x04, 4 addon bytes, then payload "hi"
	r := bytes.NewReader([]byte{0x00, 0x04, 1, 2, 3, 4, 'h', 'i'})
	if err := readResponse(r); err != nil {
		t.Fatal(err)
	}
	rest := bytes.NewBuffer(nil)
	_, _ = rest.ReadFrom(r)
	if rest.String() != "hi" {
		t.Errorf("leftover = %q, want hi", rest.String())
	}
}

func TestReadResponseShortInput(t *testing.T) {
	if err := readResponse(bytes.NewReader([]byte{0x00})); err == nil {
		t.Error("expected error for truncated header")
	}
}
