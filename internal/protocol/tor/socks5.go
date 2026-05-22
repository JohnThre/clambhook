package tor

import (
	"errors"
	"fmt"
	"io"

	"github.com/JohnThre/clambhook/internal/socks"
)

// SOCKS5 method and reply constants (RFC 1928 §3, RFC 1929 §2).
const (
	socksVersion   byte = 0x05
	methodNoAuth   byte = 0x00
	methodUserPass byte = 0x02
	methodNone     byte = 0xFF

	cmdConnect byte = 0x01
	rsvZero    byte = 0x00

	userPassVersion byte = 0x01
	userPassSuccess byte = 0x00
)

// socks5Connect performs a SOCKS5 handshake + CONNECT request against rw and
// returns nil on success. After success, rw carries the bidirectional
// byte stream to address through the SOCKS5 server.
//
// Tor accepts both the no-auth method and RFC 1929 user/pass; when user is
// non-empty we offer both, which lets Tor apply stream isolation based on
// the credential pair (see torrc SOCKSPort IsolateSOCKSAuth). When user is
// empty we offer only no-auth to keep the wire shorter.
//
// address is encoded via internal/socks.EncodeAddr, which selects
// ATYPDomain for non-IP hosts — this is what .onion addresses need: the
// SOCKS5 server (Tor) resolves the name in its own circuit, not us.
func socks5Connect(rw io.ReadWriter, address, user, pass string) error {
	if err := writeMethodGreeting(rw, user != ""); err != nil {
		return err
	}
	method, err := readMethodReply(rw)
	if err != nil {
		return err
	}
	switch method {
	case methodNoAuth:
		// nothing to do
	case methodUserPass:
		if user == "" {
			return errors.New("tor: server selected user/pass but no credentials configured")
		}
		if err := userPassAuth(rw, user, pass); err != nil {
			return err
		}
	case methodNone:
		return errors.New("tor: SOCKS5 server rejected all offered methods")
	default:
		return fmt.Errorf("tor: SOCKS5 server selected unsupported method %#x", method)
	}

	addr, err := socks.EncodeAddr(address)
	if err != nil {
		return fmt.Errorf("tor: encode target: %w", err)
	}
	req := make([]byte, 0, 3+len(addr))
	req = append(req, socksVersion, cmdConnect, rsvZero)
	req = append(req, addr...)
	if _, err := rw.Write(req); err != nil {
		return fmt.Errorf("tor: write CONNECT: %w", err)
	}

	return readConnectReply(rw)
}

func writeMethodGreeting(w io.Writer, withUserPass bool) error {
	var greet []byte
	if withUserPass {
		greet = []byte{socksVersion, 0x02, methodNoAuth, methodUserPass}
	} else {
		greet = []byte{socksVersion, 0x01, methodNoAuth}
	}
	if _, err := w.Write(greet); err != nil {
		return fmt.Errorf("tor: write greeting: %w", err)
	}
	return nil
}

func readMethodReply(r io.Reader) (byte, error) {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, fmt.Errorf("tor: read method reply: %w", err)
	}
	if b[0] != socksVersion {
		return 0, fmt.Errorf("tor: bad SOCKS version %#x in method reply", b[0])
	}
	return b[1], nil
}

// userPassAuth runs RFC 1929 sub-negotiation. Tor accepts any non-empty
// credential pair; it treats unique pairs as stream-isolation tokens
// rather than authenticating against a user database.
func userPassAuth(rw io.ReadWriter, user, pass string) error {
	if len(user) > 255 || len(pass) > 255 {
		return errors.New("tor: SOCKS5 user or pass exceeds 255 bytes")
	}
	msg := make([]byte, 0, 3+len(user)+len(pass))
	msg = append(msg, userPassVersion, byte(len(user)))
	msg = append(msg, user...)
	msg = append(msg, byte(len(pass)))
	msg = append(msg, pass...)
	if _, err := rw.Write(msg); err != nil {
		return fmt.Errorf("tor: write userpass: %w", err)
	}
	var reply [2]byte
	if _, err := io.ReadFull(rw, reply[:]); err != nil {
		return fmt.Errorf("tor: read userpass reply: %w", err)
	}
	if reply[0] != userPassVersion {
		return fmt.Errorf("tor: bad userpass version %#x", reply[0])
	}
	if reply[1] != userPassSuccess {
		return fmt.Errorf("tor: userpass auth failed (status %#x)", reply[1])
	}
	return nil
}

// readConnectReply reads and validates the SOCKS5 CONNECT reply. The
// BND.ADDR/BND.PORT fields are discarded: Tor sets them to zero, and
// legitimate SOCKS5 proxies sometimes return the exit address which we
// don't need to act on. We just need to drain them so the stream is
// positioned at the start of payload.
func readConnectReply(r io.Reader) error {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return fmt.Errorf("tor: read CONNECT reply header: %w", err)
	}
	if head[0] != socksVersion {
		return fmt.Errorf("tor: bad SOCKS version %#x in CONNECT reply", head[0])
	}
	// Always drain BND.ADDR + BND.PORT, even on error. Real SOCKS5 servers
	// send the full reply with zeroed BND fields on failure; draining keeps
	// the peer from blocking on its write and lets the caller keep (or
	// recycle) the underlying stream without bytes stuck in-flight.
	var addrLen int
	switch head[3] {
	case socks.ATYPIPv4:
		addrLen = 4
	case socks.ATYPIPv6:
		addrLen = 16
	case socks.ATYPDomain:
		var l [1]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return fmt.Errorf("tor: read bnd domain len: %w", err)
		}
		addrLen = int(l[0])
	default:
		return fmt.Errorf("tor: unknown ATYP %#x in CONNECT reply", head[3])
	}
	buf := make([]byte, addrLen+2) // addr + port
	if _, err := io.ReadFull(r, buf); err != nil {
		return fmt.Errorf("tor: drain CONNECT reply tail: %w", err)
	}
	if head[1] != 0x00 {
		return fmt.Errorf("tor: CONNECT failed: %s", replyCodeString(head[1]))
	}
	return nil
}

// replyCodeString maps RFC 1928 §6 reply codes to human-readable messages.
// Worth the verbose table: a user staring at "tor: CONNECT failed: host
// unreachable" can immediately tell the issue is network/DNS reach, not
// auth or protocol framing.
func replyCodeString(code byte) string {
	switch code {
	case 0x01:
		return "general SOCKS server failure"
	case 0x02:
		return "connection not allowed by ruleset"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused"
	case 0x06:
		return "TTL expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	default:
		return fmt.Sprintf("unknown SOCKS5 reply code %#x", code)
	}
}
