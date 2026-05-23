package vmess

import (
	"crypto/sha256"
	"io"
	"net"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/protocol"
)

// conn wraps a VMess session over an io.ReadWriteCloser (TCP, TCP+TLS, or a
// chained transport). It holds one chunk codec per direction and defers the
// response-header handshake to the first Read so Dial doesn't block on
// server I/O.
type conn struct {
	rwc        io.ReadWriteCloser
	writeCodec *chunkCodec
	state      requestState
	security   byte

	writeMu sync.Mutex

	readOnce  sync.Once
	readErr   error
	readCodec *chunkCodec
	pending   []byte

	readMu sync.Mutex
}

func newConn(rwc io.ReadWriteCloser, state requestState, security byte) (*conn, error) {
	aead, err := newBodyAEAD(security, state.reqKey[:])
	if err != nil {
		return nil, err
	}
	return &conn{
		rwc:        rwc,
		writeCodec: newChunkCodec(aead, state.reqIV),
		state:      state,
		security:   security,
	}, nil
}

func (c *conn) Protocol() string { return "vmess" }

func (c *conn) ensureReadCodec() error {
	c.readOnce.Do(func() {
		if _, err := readResponse(c.rwc, c.state); err != nil {
			c.readErr = err
			return
		}
		// Response-side body keys: SHA-256 of the request-side counterparts.
		respKey := sha256.Sum256(c.state.reqKey[:])
		respIV := sha256.Sum256(c.state.reqIV[:])
		aead, err := newBodyAEAD(c.security, respKey[:16])
		if err != nil {
			c.readErr = err
			return
		}
		var iv [16]byte
		copy(iv[:], respIV[:16])
		c.readCodec = newChunkCodec(aead, iv)
	})
	return c.readErr
}

// Read returns decrypted body bytes. The response header is parsed on the
// first call so writes can go out before the server has responded.
func (c *conn) Read(p []byte) (int, error) {
	if err := c.ensureReadCodec(); err != nil {
		return 0, err
	}

	c.readMu.Lock()
	defer c.readMu.Unlock()

	if len(c.pending) == 0 {
		chunk, err := c.readCodec.open(c.rwc)
		if err != nil {
			return 0, err
		}
		c.pending = chunk
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *conn) readChunk() ([]byte, error) {
	if err := c.ensureReadCodec(); err != nil {
		return nil, err
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	return c.readCodec.open(c.rwc)
}

// Write splits p across chunks if it exceeds maxChunkPlaintext.
func (c *conn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > maxChunkPlaintext {
			chunk = chunk[:maxChunkPlaintext]
		}
		n, err := c.writeCodec.seal(c.rwc, chunk)
		if err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

func (c *conn) writeChunk(p []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.writeCodec.seal(c.rwc, p)
	return err
}

func (c *conn) Close() error { return c.rwc.Close() }

// LocalAddr/RemoteAddr/SetDeadline* delegate to rwc if it's a net.Conn.
func (c *conn) LocalAddr() net.Addr {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.LocalAddr()
	}
	return dummyAddr{}
}

func (c *conn) RemoteAddr() net.Addr {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.RemoteAddr()
	}
	return dummyAddr{}
}

func (c *conn) SetDeadline(t time.Time) error {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.SetDeadline(t)
	}
	return nil
}

func (c *conn) SetReadDeadline(t time.Time) error {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.SetReadDeadline(t)
	}
	return nil
}

func (c *conn) SetWriteDeadline(t time.Time) error {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.SetWriteDeadline(t)
	}
	return nil
}

// Compile-time guards — accidental signature drift fails at build time.
var (
	_ protocol.Conn = (*conn)(nil)
	_ net.Conn      = (*conn)(nil)
)
