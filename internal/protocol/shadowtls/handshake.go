// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package shadowtls

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"io"
	"net"
	"time"
)

// TLS record and handshake layout constants (see RFC 8446 §5.1, §4.1.2/§4.1.3).
const (
	tlsRandomSize    = 32
	tlsHeaderSize    = 5
	tlsSessionIDSize = 32
	hmacSize         = 4

	recordHandshake       = 22
	recordApplicationData = 23

	handshakeClientHello = 1
	handshakeServerHello = 2

	// Offset of the 32-byte Random field within a handshake record body:
	//   record header(5) + msg type(1) + length(3) + legacy_version(2).
	serverRandomIndex = tlsHeaderSize + 1 + 3 + 2
	// Offset of the session_id length byte, right after Random.
	sessionIDLengthIndex = serverRandomIndex + tlsRandomSize
	// A full data-stage frame header is the TLS record header + inner HMAC.
	tlsHmacHeaderSize = tlsHeaderSize + hmacSize
)

// hijackConn sits under crypto/tls.Client for the read side of the handshake.
// The client's HMAC-signed legacy_session_id is injected upstream via the
// deterministic two-pass Rand mechanism (see sessionid.go), so hijackConn does
// not touch the ClientHello. On the read side it captures ServerRandom from the
// ServerHello, detects TLS 1.3, and — per the v3 spec — verifies that the
// server rewrites its ApplicationData frames with a ServerRandom-keyed HMAC
// (proving the server is a genuine ShadowTLS peer and not a hijacked path),
// un-XORing them so crypto/tls sees the real handshake records.
//
// After the TLS handshake completes, the data stage abandons the tls.Conn and
// speaks directly to the raw transport, so hijackConn is only ever used during
// the handshake.
type hijackConn struct {
	net.Conn
	password string

	// read side
	buf          []byte // pending plaintext to hand back to tls.Client
	serverRandom []byte
	readHMAC     hash.Hash // HMAC_ServerRandom, reused to filter residual frames
	readHMACKey  []byte    // SHA256(password || serverRandom), the XOR keystream
	isTLS13      bool
	authorized   bool
}

func newHijackConn(conn net.Conn, password string) *hijackConn {
	return &hijackConn{Conn: conn, password: password}
}

func (c *hijackConn) Read(p []byte) (int, error) {
	if len(c.buf) > 0 {
		n := copy(p, c.buf)
		c.buf = c.buf[n:]
		return n, nil
	}

	var header [tlsHeaderSize]byte
	if _, err := io.ReadFull(c.Conn, header[:]); err != nil {
		return 0, err
	}
	length := int(binary.BigEndian.Uint16(header[3:tlsHeaderSize]))
	frame := make([]byte, tlsHeaderSize+length)
	copy(frame, header[:])
	if _, err := io.ReadFull(c.Conn, frame[tlsHeaderSize:]); err != nil {
		return 0, err
	}

	switch header[0] {
	case recordHandshake:
		if len(frame) > serverRandomIndex+tlsRandomSize && frame[tlsHeaderSize] == handshakeServerHello {
			c.serverRandom = make([]byte, tlsRandomSize)
			copy(c.serverRandom, frame[serverRandomIndex:serverRandomIndex+tlsRandomSize])
			c.readHMAC = hmac.New(sha1.New, []byte(c.password))
			c.readHMAC.Write(c.serverRandom)
			c.readHMACKey = kdf(c.password, c.serverRandom)
			c.isTLS13 = serverHelloSupportsTLS13(frame[tlsHeaderSize:])
			// For TLS 1.2 the server does not rewrite ApplicationData, so it
			// is considered authorized on the ServerHello alone. Strict v3
			// (enforced by the dialer) rejects non-1.3 anyway.
			if !c.isTLS13 {
				c.authorized = true
			}
		}
	case recordApplicationData:
		// Every genuine ShadowTLS ApplicationData frame is HMAC-rewritten by
		// the server. Absence of a valid HMAC means the path is not a real
		// ShadowTLS peer (or is hijacked): mark unauthorized.
		c.authorized = false
		if len(frame) > tlsHmacHeaderSize && c.readHMAC != nil {
			c.readHMAC.Write(frame[tlsHmacHeaderSize:])
			if hmac.Equal(c.readHMAC.Sum(nil)[:hmacSize], frame[tlsHeaderSize:tlsHmacHeaderSize]) {
				// Undo the server's XOR so the plaintext TLS record is restored
				// for crypto/tls, and strip the 4-byte inner HMAC.
				xorSlice(frame[tlsHmacHeaderSize:], c.readHMACKey)
				copy(frame[hmacSize:], frame[:tlsHeaderSize])
				binary.BigEndian.PutUint16(frame[hmacSize+3:], uint16(len(frame)-tlsHmacHeaderSize))
				frame = frame[hmacSize:]
				c.authorized = true
			}
		}
	}

	c.buf = frame
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

// SetDeadline / SetReadDeadline / SetWriteDeadline / LocalAddr / RemoteAddr are
// inherited from the embedded net.Conn (netConnAdapter supplies sane defaults
// when the transport is a chained io.ReadWriteCloser).

// kdf derives the XOR keystream the server applies to handshake-relay
// ApplicationData: SHA256(password || serverRandom).
func kdf(password string, serverRandom []byte) []byte {
	h := sha256.New()
	h.Write([]byte(password))
	h.Write(serverRandom)
	return h.Sum(nil)
}

func xorSlice(data, key []byte) {
	for i := range data {
		data[i] ^= key[i%len(key)]
	}
}

// serverHelloSupportsTLS13 parses the ServerHello body (starting at the msg
// type byte) and reports whether the supported_versions extension (type 43)
// selects TLS 1.3 (0x0304).
func serverHelloSupportsTLS13(frame []byte) bool {
	// frame indexes are relative to the record body; sessionIDLengthIndex is
	// relative to the whole record, so subtract the header size.
	const sidLenIdx = sessionIDLengthIndex - tlsHeaderSize
	if len(frame) < sidLenIdx+1 {
		return false
	}
	r := bytes.NewReader(frame[sidLenIdx:])

	var sidLen uint8
	if err := binary.Read(r, binary.BigEndian, &sidLen); err != nil {
		return false
	}
	if _, err := io.CopyN(io.Discard, r, int64(sidLen)); err != nil {
		return false
	}
	// cipher_suite(2) + compression_method(1).
	if _, err := io.CopyN(io.Discard, r, 3); err != nil {
		return false
	}
	var extListLen uint16
	if err := binary.Read(r, binary.BigEndian, &extListLen); err != nil {
		return false
	}
	for {
		var extType, extLen uint16
		if err := binary.Read(r, binary.BigEndian, &extType); err != nil {
			return false
		}
		if err := binary.Read(r, binary.BigEndian, &extLen); err != nil {
			return false
		}
		if extType != 43 {
			if _, err := io.CopyN(io.Discard, r, int64(extLen)); err != nil {
				return false
			}
			continue
		}
		if extLen != 2 {
			return false
		}
		var value uint16
		if err := binary.Read(r, binary.BigEndian, &value); err != nil {
			return false
		}
		return value == 0x0304
	}
}

type protocolError struct{ msg string }

func (e *protocolError) Error() string { return "shadowtls: " + e.msg }

// netConnAdapter adapts a chained io.ReadWriteCloser (from DialThrough) to
// net.Conn so it can sit under crypto/tls and the data-stage conn. Mirrors the
// adapter used by trojanwire/vmess.
type netConnAdapter struct {
	rwc io.ReadWriteCloser
}

func (a *netConnAdapter) Read(p []byte) (int, error)  { return a.rwc.Read(p) }
func (a *netConnAdapter) Write(p []byte) (int, error) { return a.rwc.Write(p) }
func (a *netConnAdapter) Close() error                { return a.rwc.Close() }

func (a *netConnAdapter) LocalAddr() net.Addr {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.LocalAddr()
	}
	return dummyAddr{}
}

func (a *netConnAdapter) RemoteAddr() net.Addr {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.RemoteAddr()
	}
	return dummyAddr{}
}

func (a *netConnAdapter) SetDeadline(t time.Time) error {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.SetDeadline(t)
	}
	return nil
}

func (a *netConnAdapter) SetReadDeadline(t time.Time) error {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.SetReadDeadline(t)
	}
	return nil
}

func (a *netConnAdapter) SetWriteDeadline(t time.Time) error {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.SetWriteDeadline(t)
	}
	return nil
}

type dummyAddr struct{}

func (dummyAddr) Network() string { return "shadowtls-chain" }
func (dummyAddr) String() string  { return "chained" }
