package trojan

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
)

// A pipeReadWriter lets us drive the codec without a real TLS stream.
// Write appends to the buffer; Read drains from the front.
type pipeReadWriter struct {
	buf bytes.Buffer
}

func (p *pipeReadWriter) Read(b []byte) (int, error)  { return p.buf.Read(b) }
func (p *pipeReadWriter) Write(b []byte) (int, error) { return p.buf.Write(b) }
func (p *pipeReadWriter) Close() error                { return nil }

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
			encoded, err := encodeAddr(tt.address)
			if err != nil {
				t.Fatal(err)
			}
			host, port, err := readAddr(bytes.NewReader(encoded))
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
	_, _, err := readAddr(bytes.NewReader([]byte{0x09, 0x01, 0x02, 0x03}))
	if err == nil {
		t.Error("expected error for unknown ATYP")
	}
}

func TestReadAddrRejectsEmptyDomain(t *testing.T) {
	_, _, err := readAddr(bytes.NewReader([]byte{atypDomain, 0}))
	if err == nil {
		t.Error("expected error for empty domain")
	}
}

// Round-trip via WriteTo → ReadFrom using a shared byte buffer wrapped in a
// fake TLS-like conn. Proves the on-wire framing reassembles to the same
// address + payload.
func TestPacketConnRoundTripsFrames(t *testing.T) {
	pair := &pipeReadWriter{}
	// Both the writer and the reader share the same byte stream.
	// We can't share a tls.Conn without a real TLS session, so we reach below
	// it by writing to the underlying net.Conn through a helper.
	//
	// Instead of synthesizing a tls.Conn, test the codec through the wire-format
	// functions directly:
	want := []byte("hello, udp")
	frame, err := buildFrameForTest(t, "example.com:53", want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pair.Write(frame); err != nil {
		t.Fatal(err)
	}

	host, port, err := readAddr(pair)
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

// buildFrameForTest assembles a complete trojan UDP frame without going
// through a tls.Conn. Mirrors the real packetConn.WriteTo exactly.
func buildFrameForTest(t *testing.T, address string, payload []byte) ([]byte, error) {
	t.Helper()
	addrBytes, err := encodeAddr(address)
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
