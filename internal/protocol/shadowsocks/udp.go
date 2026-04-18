package shadowsocks

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/clambhook/clambhook/internal/protocol"
	"github.com/clambhook/clambhook/internal/socks"
)

// Shadowsocks AEAD UDP is a one-shot per-packet AEAD, not the chunked-stream
// framing used over TCP. Each datagram on the wire is:
//
//	[salt(saltSize)] [enc(ATYP || ADDR || PORT || payload) || tag(16)]
//
// The nonce is 12 zero bytes — correct because the per-packet salt makes
// each subkey unique, so the (subkey, nonce) pair never repeats across
// datagrams in the same connection. A per-packet random salt is the primary
// defense against replay; tagging catches tampering.

// maxUDPPacket is a generous cap on how large a single SS-AEAD datagram can
// be. Real SS traffic rarely exceeds the IP MTU (~1500), but proxies on
// loopback or with fragmentation can produce larger payloads.
const maxUDPPacket = 65535

// DialPacket opens a UDP-carrying Shadowsocks session directly to the
// configured server. The address argument is ignored — SS-AEAD UDP carries
// the per-datagram destination in the packet header (via WriteTo's addr
// argument), so there's no connection-scoped target.
func (d *dialer) DialPacket(ctx context.Context, address string) (protocol.PacketConn, error) {
	// Resolve and connect a UDP socket. We use Dial rather than ListenPacket
	// so the kernel filters inbound packets to just those from the server —
	// any spoofed traffic from elsewhere is dropped before we waste CPU on it.
	var ld net.Dialer
	raw, err := ld.DialContext(ctx, "udp", d.server.Address)
	if err != nil {
		return nil, fmt.Errorf("shadowsocks: dial udp %s: %w", d.server.Address, err)
	}
	udp, ok := raw.(*net.UDPConn)
	if !ok {
		raw.Close()
		return nil, fmt.Errorf("shadowsocks: unexpected conn type %T", raw)
	}
	return &packetConn{
		udp: udp,
		cfg: &d.cfg,
	}, nil
}

// DialPacketThrough is declined for SS: per-packet AEAD requires datagram
// semantics, which an upstream stream-tunnel can't reliably preserve without
// explicit framing beyond SS's spec. Surface a loud error so callers fail
// fast rather than silently dropping traffic.
func (d *dialer) DialPacketThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.PacketConn, error) {
	_ = underlying
	return nil, errors.New("shadowsocks: UDP over a tunneled stream is not supported")
}

type packetConn struct {
	udp *net.UDPConn
	cfg *config

	readMu  sync.Mutex
	writeMu sync.Mutex
}

func (p *packetConn) Protocol() string { return "shadowsocks" }

// WriteTo sends payload to addr through the Shadowsocks server. Frame layout:
//
//	[salt] || [enc(addrBytes || payload) || tag]
//
// Each packet uses a fresh CSPRNG salt, which uniquely determines the AEAD
// subkey; nonce is zero. Returns the number of PAYLOAD bytes written, per
// net.PacketConn convention (not the wire size).
func (p *packetConn) WriteTo(payload []byte, addr net.Addr) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	addrBytes, err := socks.EncodeAddr(addr.String())
	if err != nil {
		return 0, fmt.Errorf("shadowsocks: encode target: %w", err)
	}

	salt := make([]byte, p.cfg.spec.saltSize)
	if _, err := rand.Read(salt); err != nil {
		return 0, fmt.Errorf("shadowsocks: generate salt: %w", err)
	}

	subkey := hkdfSHA1(p.cfg.masterKey, salt, ssSubkeyInfo, p.cfg.spec.keySize)

	plaintext := make([]byte, 0, len(addrBytes)+len(payload))
	plaintext = append(plaintext, addrBytes...)
	plaintext = append(plaintext, payload...)

	zeroNonce := make([]byte, p.cfg.spec.nonceSize)
	ct, tag, err := p.cfg.spec.encrypt(subkey, zeroNonce, plaintext, nil)
	if err != nil {
		return 0, fmt.Errorf("shadowsocks: encrypt datagram: %w", err)
	}

	frame := make([]byte, 0, len(salt)+len(ct)+len(tag))
	frame = append(frame, salt...)
	frame = append(frame, ct...)
	frame = append(frame, tag...)

	if _, err := p.udp.Write(frame); err != nil {
		return 0, err
	}
	return len(payload), nil
}

// ReadFrom receives a single SS-AEAD datagram and returns the payload plus
// the address of the upstream target that sent it (from the decrypted
// header). Per net.PacketConn semantics, if buf is smaller than the payload
// the remainder is discarded.
func (p *packetConn) ReadFrom(buf []byte) (int, net.Addr, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()

	wire := make([]byte, maxUDPPacket)
	n, err := p.udp.Read(wire)
	if err != nil {
		return 0, nil, err
	}
	wire = wire[:n]

	saltSize := p.cfg.spec.saltSize
	tagSize := p.cfg.spec.tagSize
	if n < saltSize+tagSize {
		return 0, nil, fmt.Errorf("shadowsocks: datagram too short (%d bytes)", n)
	}

	salt := wire[:saltSize]
	tag := wire[n-tagSize:]
	ct := wire[saltSize : n-tagSize]

	subkey := hkdfSHA1(p.cfg.masterKey, salt, ssSubkeyInfo, p.cfg.spec.keySize)
	zeroNonce := make([]byte, p.cfg.spec.nonceSize)
	plaintext, err := p.cfg.spec.decrypt(subkey, zeroNonce, ct, nil, tag)
	if err != nil {
		return 0, nil, fmt.Errorf("shadowsocks: decrypt datagram: %w", err)
	}

	host, port, err := socks.ReadAddr(bytes.NewReader(plaintext))
	if err != nil {
		return 0, nil, fmt.Errorf("shadowsocks: parse address: %w", err)
	}
	// The rest of the plaintext after the address triple is the payload.
	// Recompute the offset via socks.EncodeAddr — cheaper than tracking
	// ReadAddr's internal cursor: re-encode from (host, port) and use its
	// length. For correctness this is exact because EncodeAddr is a bijection
	// over well-formed (host, port) pairs within supported ATYPs.
	addrLen := addrLenFromPlaintext(plaintext)
	if addrLen < 0 || addrLen > len(plaintext) {
		return 0, nil, errors.New("shadowsocks: address span exceeds datagram")
	}
	payload := plaintext[addrLen:]
	copied := copy(buf, payload)
	return copied, &packetAddr{host: host, port: port}, nil
}

// addrLenFromPlaintext returns the number of bytes the ATYP|ADDR|PORT header
// consumes at the start of a decrypted datagram. We compute it structurally
// rather than threading a cursor out of socks.ReadAddr — keeps that function's
// signature simple.
func addrLenFromPlaintext(p []byte) int {
	if len(p) < 1 {
		return -1
	}
	switch p[0] {
	case socks.ATYPIPv4:
		return 1 + 4 + 2
	case socks.ATYPIPv6:
		return 1 + 16 + 2
	case socks.ATYPDomain:
		if len(p) < 2 {
			return -1
		}
		return 1 + 1 + int(p[1]) + 2
	default:
		return -1
	}
}

func (p *packetConn) Close() error {
	return p.udp.Close()
}

func (p *packetConn) LocalAddr() net.Addr {
	return p.udp.LocalAddr()
}

func (p *packetConn) SetDeadline(t time.Time) error {
	return p.udp.SetDeadline(t)
}

func (p *packetConn) SetReadDeadline(t time.Time) error {
	return p.udp.SetReadDeadline(t)
}

func (p *packetConn) SetWriteDeadline(t time.Time) error {
	return p.udp.SetWriteDeadline(t)
}

// packetAddr satisfies net.Addr for the remote peer a datagram targets.
// Mirrors the trojan UDP implementation.
type packetAddr struct {
	host string
	port uint16
}

func (p *packetAddr) Network() string { return "udp" }
func (p *packetAddr) String() string {
	return net.JoinHostPort(p.host, fmt.Sprintf("%d", p.port))
}

var (
	_ protocol.PacketDialer = (*dialer)(nil)
	_ protocol.PacketConn   = (*packetConn)(nil)
	_ net.PacketConn        = (*packetConn)(nil)
)
