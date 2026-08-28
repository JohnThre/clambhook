// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package shadowtls

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
	"net"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/protocol"
)

// maxDataChunk bounds a single ApplicationData record's payload, matching the
// TLS record size limit used by the reference ShadowTLS implementation.
const maxDataChunk = 16384

// stConn is the ShadowTLS v3 data-stage connection. It speaks directly to the
// raw transport (the TLS library is abandoned after the handshake) and applies
// the authenticated ApplicationData framing:
//
//	(5B TLS record header)(4B HMAC)(payload)
//
// Writes are authenticated with hmacAdd (HMAC keyed by ServerRandom||"C");
// reads are verified with hmacVerify (ServerRandom||"S"). hmacIgnore
// (HMAC_ServerRandom) filters residual handshake frames the server may still
// emit after the client switches to the data stage.
type stConn struct {
	rwc io.ReadWriteCloser

	writeMu sync.Mutex
	hmacAdd hash.Hash

	readMu     sync.Mutex
	hmacVerify hash.Hash
	hmacIgnore hash.Hash
	readBuf    []byte
}

func newStConn(rwc io.ReadWriteCloser, hmacAdd, hmacVerify, hmacIgnore hash.Hash) *stConn {
	return &stConn{
		rwc:        rwc,
		hmacAdd:    hmacAdd,
		hmacVerify: hmacVerify,
		hmacIgnore: hmacIgnore,
	}
}

func (c *stConn) Protocol() string { return "shadowtls" }

func (c *stConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	if len(c.readBuf) > 0 {
		n := copy(p, c.readBuf)
		c.readBuf = c.readBuf[n:]
		return n, nil
	}

	for {
		var header [tlsHeaderSize]byte
		if _, err := io.ReadFull(c.rwc, header[:]); err != nil {
			return 0, err
		}
		length := int(binary.BigEndian.Uint16(header[3:tlsHeaderSize]))
		frame := make([]byte, tlsHeaderSize+length)
		copy(frame, header[:])
		if _, err := io.ReadFull(c.rwc, frame[tlsHeaderSize:]); err != nil {
			return 0, err
		}

		switch header[0] {
		case recordAlert:
			return 0, fmt.Errorf("shadowtls: remote alert")
		case recordApplicationData:
			// Residual handshake data (HMAC_ServerRandom) is filtered out until
			// the first real data frame switches the stream over.
			if c.hmacIgnore != nil {
				if verifyFrame(frame, c.hmacIgnore, false) {
					continue
				}
				c.hmacIgnore = nil
			}
			if !verifyFrame(frame, c.hmacVerify, true) {
				c.sendAlert()
				return 0, fmt.Errorf("shadowtls: application data verification failed")
			}
			c.readBuf = frame[tlsHmacHeaderSize:]
			n := copy(p, c.readBuf)
			c.readBuf = c.readBuf[n:]
			return n, nil
		default:
			c.sendAlert()
			return 0, fmt.Errorf("shadowtls: unexpected TLS record type %d", header[0])
		}
	}
}

func (c *stConn) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		chunk := p
		if len(chunk) > maxDataChunk {
			chunk = chunk[:maxDataChunk]
		}
		if err := c.writeChunk(chunk); err != nil {
			return 0, err
		}
		p = p[len(chunk):]
	}
	return total, nil
}

func (c *stConn) writeChunk(p []byte) error {
	var header [tlsHmacHeaderSize]byte
	header[0] = recordApplicationData
	header[1] = 3
	header[2] = 3
	binary.BigEndian.PutUint16(header[3:tlsHeaderSize], uint16(hmacSize+len(p)))

	c.writeMu.Lock()
	c.hmacAdd.Write(p)
	sum := c.hmacAdd.Sum(nil)[:hmacSize]
	// Feed the emitted HMAC back into the instance so consecutive frames chain,
	// defeating cut-and-splice tampering.
	c.hmacAdd.Write(sum)
	c.writeMu.Unlock()

	copy(header[tlsHeaderSize:], sum)

	frame := make([]byte, 0, len(header)+len(p))
	frame = append(frame, header[:]...)
	frame = append(frame, p...)
	if _, err := c.rwc.Write(frame); err != nil {
		return fmt.Errorf("shadowtls: write frame: %w", err)
	}
	return nil
}

func (c *stConn) sendAlert() {
	const recordSize = 31
	record := [recordSize]byte{recordAlert, 3, 3, 0, recordSize - tlsHeaderSize}
	if _, err := rand.Read(record[tlsHeaderSize:]); err != nil {
		return
	}
	_, _ = c.rwc.Write(record[:])
}

func (c *stConn) Close() error { return c.rwc.Close() }

func (c *stConn) LocalAddr() net.Addr {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.LocalAddr()
	}
	return dummyAddr{}
}

func (c *stConn) RemoteAddr() net.Addr {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.RemoteAddr()
	}
	return dummyAddr{}
}

func (c *stConn) SetDeadline(t time.Time) error {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.SetDeadline(t)
	}
	return nil
}

func (c *stConn) SetReadDeadline(t time.Time) error {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.SetReadDeadline(t)
	}
	return nil
}

func (c *stConn) SetWriteDeadline(t time.Time) error {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.SetWriteDeadline(t)
	}
	return nil
}

// verifyFrame checks a data-stage ApplicationData frame's 4-byte inner HMAC.
// When update is true, the verified HMAC bytes are chained back into the
// instance (matching the writer's chaining) so subsequent frames validate.
func verifyFrame(frame []byte, mac hash.Hash, update bool) bool {
	if len(frame) < tlsHmacHeaderSize || frame[1] != 3 || frame[2] != 3 {
		return false
	}
	mac.Write(frame[tlsHmacHeaderSize:])
	sum := mac.Sum(nil)[:hmacSize]
	if update {
		mac.Write(sum)
	}
	return hmac.Equal(sum, frame[tlsHeaderSize:tlsHmacHeaderSize])
}

const recordAlert = 21

var _ protocol.Conn = (*stConn)(nil)
