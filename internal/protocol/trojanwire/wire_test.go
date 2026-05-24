package trojanwire

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"strconv"
	"testing"

	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/pkg/cnet"
)

type pipeReadWriter struct {
	buf bytes.Buffer
}

func (p *pipeReadWriter) Read(b []byte) (int, error)  { return p.buf.Read(b) }
func (p *pipeReadWriter) Write(b []byte) (int, error) { return p.buf.Write(b) }
func (p *pipeReadWriter) Close() error                { return nil }

func TestSHA224RFC3874Vector(t *testing.T) {
	got := cnet.SHA224([]byte("abc"))
	want, _ := hex.DecodeString("23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7")
	if !bytes.Equal(got, want) {
		t.Fatalf("SHA-224(\"abc\") mismatch\n got=%x\nwant=%x", got, want)
	}
}

func TestEncodeAddrIPv4(t *testing.T) {
	got, err := EncodeAddr("trojan", "1.2.3.4:80")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{ATYPIPv4, 1, 2, 3, 4, 0x00, 0x50}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestEncodeAddrIPv6(t *testing.T) {
	got, err := EncodeAddr("trojan", "[2001:db8::1]:443")
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
	got, err := EncodeAddr("trojan", "example.com:443")
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
	cases := []string{"", "example.com", "example.com:99999", "example.com:notanum"}
	for _, c := range cases {
		if _, err := EncodeAddr("trojan", c); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestEncodeAddrRejectsLongDomain(t *testing.T) {
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := EncodeAddr("trojan", string(long)+":443"); err == nil {
		t.Error("expected error for domain > 255 bytes")
	}
}

func TestEncodeHeaderFullBytes(t *testing.T) {
	var hashHex [56]byte
	sum := cnet.SHA224([]byte("secret"))
	hex.Encode(hashHex[:], sum)

	got, err := EncodeHeader("trojan", hashHex, CmdConnect, "1.2.3.4:80")
	if err != nil {
		t.Fatal(err)
	}

	want := make([]byte, 0, 56+2+8+2)
	want = append(want, hashHex[:]...)
	want = append(want, '\r', '\n')
	want = append(want, CmdConnect, ATYPIPv4, 1, 2, 3, 4, 0x00, 0x50)
	want = append(want, '\r', '\n')

	if !bytes.Equal(got, want) {
		t.Fatalf("header mismatch\n got=%x\nwant=%x", got, want)
	}
}

func TestEncodeHeaderUDPAssociate(t *testing.T) {
	var hashHex [56]byte
	sum := cnet.SHA224([]byte("secret"))
	hex.Encode(hashHex[:], sum)

	got, err := EncodeHeader("trojan", hashHex, CmdUDPAssociate, "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	if got[58] != CmdUDPAssociate {
		t.Errorf("cmd byte = %#x, want %#x", got[58], CmdUDPAssociate)
	}
	if got[59] != ATYPIPv4 {
		t.Errorf("atyp = %#x, want %#x", got[59], ATYPIPv4)
	}
}

func TestParseConfigMissingPassword(t *testing.T) {
	_, err := ParseConfig("trojan", protocol.Server{Address: "example.com:443", Settings: map[string]any{}})
	if err == nil {
		t.Fatal("expected error for missing password")
	}
}

func TestParseConfigSNIDefaultsToHost(t *testing.T) {
	cfg, err := ParseConfig("trojan", protocol.Server{
		Address:  "example.com:443",
		Settings: map[string]any{"password": "hunter2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SNI != "example.com" {
		t.Fatalf("SNI = %q, want %q", cfg.SNI, "example.com")
	}
}

func TestParseConfigSNIExplicit(t *testing.T) {
	cfg, err := ParseConfig("trojan", protocol.Server{
		Address: "203.0.113.5:443",
		Settings: map[string]any{
			"password": "hunter2",
			"sni":      "cloud.example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SNI != "cloud.example.com" {
		t.Fatalf("SNI = %q, want %q", cfg.SNI, "cloud.example.com")
	}
}

func TestParseConfigPrecomputesHashHex(t *testing.T) {
	cfg, err := ParseConfig("trojan", protocol.Server{
		Address:  "example.com:443",
		Settings: map[string]any{"password": "hunter2"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var want [56]byte
	hex.Encode(want[:], cnet.SHA224([]byte("hunter2")))
	if cfg.PasswordHashHex != want {
		t.Fatalf("hash hex = %s, want %s", cfg.PasswordHashHex, want)
	}
}

func TestReadAddrRoundTrips(t *testing.T) {
	tests := []struct {
		name    string
		address string
	}{
		{"ipv4", "1.2.3.4:80"},
		{"ipv6", "[2001:db8::1]:443"},
		{"domain", "example.com:8080"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := EncodeAddr("trojan", tt.address)
			if err != nil {
				t.Fatal(err)
			}
			host, port, err := ReadAddr("trojan", bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}
			got := net.JoinHostPort(host, strconv.Itoa(int(port)))
			if got != tt.address {
				t.Errorf("got %q, want %q", got, tt.address)
			}
		})
	}
}

func TestReadAddrRejectsUnknownATYP(t *testing.T) {
	_, _, err := ReadAddr("trojan", bytes.NewReader([]byte{0x09, 0x01, 0x02, 0x03}))
	if err == nil {
		t.Error("expected error for unknown ATYP")
	}
}

func TestReadAddrRejectsEmptyDomain(t *testing.T) {
	_, _, err := ReadAddr("trojan", bytes.NewReader([]byte{ATYPDomain, 0}))
	if err == nil {
		t.Error("expected error for empty domain")
	}
}

func TestPacketConnRoundTripsFrames(t *testing.T) {
	pair := &pipeReadWriter{}
	want := []byte("hello, udp")
	frame, err := buildFrameForTest(t, "example.com:53", want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pair.Write(frame); err != nil {
		t.Fatal(err)
	}

	host, port, err := ReadAddr("trojan", pair)
	if err != nil {
		t.Fatal(err)
	}
	if host != "example.com" || port != 53 {
		t.Errorf("addr = %s:%d, want example.com:53", host, port)
	}

	var lb [2]byte
	if _, err := io.ReadFull(pair, lb[:]); err != nil {
		t.Fatal(err)
	}
	length := int(binary.BigEndian.Uint16(lb[:]))
	if length != len(want) {
		t.Errorf("length = %d, want %d", length, len(want))
	}
	var crlf [2]byte
	if _, err := io.ReadFull(pair, crlf[:]); err != nil {
		t.Fatal(err)
	}
	if crlf != [2]byte{'\r', '\n'} {
		t.Errorf("crlf = %v", crlf)
	}
	got := make([]byte, length)
	if _, err := io.ReadFull(pair, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("payload = %q, want %q", got, want)
	}
}

func buildFrameForTest(t *testing.T, address string, payload []byte) ([]byte, error) {
	t.Helper()
	addrBytes, err := EncodeAddr("trojan", address)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(addrBytes)+2+2+len(payload))
	out = append(out, addrBytes...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(payload)))
	out = append(out, '\r', '\n')
	out = append(out, payload...)
	return out, nil
}
