// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package vmess

import (
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

// vmessConn is a net.Conn applying VMESS-AEAD chunk framing. The write side is
// wired during handshake; the response header + read side are lazily set up on
// first Read so Dial doesn't block on server I/O.
type vmessConn struct {
	rwc  io.ReadWriteCloser
	sess *session
	cfg  *config
	cw   *chunkWriter

	readOnce sync.Once
	readErr  error
	cr       *chunkReader
}

func (c *vmessConn) Protocol() string { return "vmess" }

func (c *vmessConn) Read(p []byte) (int, error) {
	c.readOnce.Do(func() {
		if err := readResponseHeader(c.rwc, c.sess); err != nil {
			c.readErr = err
			return
		}
		cr, err := newChunkReader(c.rwc, c.cfg.security, c.sess.responseBodyKey[:], c.sess.responseBodyIV[:])
		if err != nil {
			c.readErr = err
			return
		}
		c.cr = cr
	})
	if c.readErr != nil {
		return 0, c.readErr
	}
	return c.cr.Read(p)
}

func (c *vmessConn) Write(p []byte) (int, error) { return c.cw.Write(p) }

func (c *vmessConn) Close() error { return c.rwc.Close() }

func (c *vmessConn) LocalAddr() net.Addr {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.LocalAddr()
	}
	return dummyAddr{}
}

func (c *vmessConn) RemoteAddr() net.Addr {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.RemoteAddr()
	}
	return dummyAddr{}
}

func (c *vmessConn) SetDeadline(t time.Time) error {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.SetDeadline(t)
	}
	return nil
}

func (c *vmessConn) SetReadDeadline(t time.Time) error {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.SetReadDeadline(t)
	}
	return nil
}

func (c *vmessConn) SetWriteDeadline(t time.Time) error {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.SetWriteDeadline(t)
	}
	return nil
}

// vmessPacketConn carries UDP datagrams over a VMESS body stream. Each chunk is
// one datagram; the destination is fixed by the request header (target set at
// DialPacket time), so ReadFrom/WriteTo report that single peer.
type vmessPacketConn struct {
	conn   *vmessConn
	target packetAddr

	writeMu sync.Mutex
}

func (p *vmessPacketConn) Protocol() string { return "vmess" }

func (p *vmessPacketConn) ReadFrom(buf []byte) (int, net.Addr, error) {
	n, err := p.conn.Read(buf)
	if err != nil {
		return 0, nil, err
	}
	return n, p.target, nil
}

func (p *vmessPacketConn) WriteTo(payload []byte, _ net.Addr) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := p.conn.Write(payload); err != nil {
		return 0, err
	}
	return len(payload), nil
}

func (p *vmessPacketConn) Close() error                       { return p.conn.Close() }
func (p *vmessPacketConn) LocalAddr() net.Addr                { return p.conn.LocalAddr() }
func (p *vmessPacketConn) SetDeadline(t time.Time) error      { return p.conn.SetDeadline(t) }
func (p *vmessPacketConn) SetReadDeadline(t time.Time) error  { return p.conn.SetReadDeadline(t) }
func (p *vmessPacketConn) SetWriteDeadline(t time.Time) error { return p.conn.SetWriteDeadline(t) }

type packetAddr struct {
	host string
	port int
}

func (a packetAddr) Network() string { return "udp" }
func (a packetAddr) String() string  { return net.JoinHostPort(a.host, strconv.Itoa(a.port)) }

// netConnAdapter wraps a chained io.ReadWriteCloser as a net.Conn, delegating
// deadline/addr calls to the underlying conn when it is itself a net.Conn.
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

func (dummyAddr) Network() string { return "vmess-chain" }
func (dummyAddr) String() string  { return "chained" }
