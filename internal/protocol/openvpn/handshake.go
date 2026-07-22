package openvpn

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// runHandshake drives the full OpenVPN v2 control-plane dance and
// leaves inst with a live data channel + ifconfig when it returns.
//
// Sequence:
//  1. HARD_RESET_CLIENT_V2 / HARD_RESET_SERVER_V2 over reliable.
//  2. TLS handshake using crypto/tls over the control channel.
//  3. TLS-EKM key derivation (256 bytes → 4 slots).
//  4. Client sends the "key_method=2" CONTROL_V1 message (random seeds,
//     options, optional creds, peer_info).
//  5. Read server reply (key_method 2 echo).
//  6. Read PUSH_REPLY containing cipher, peer-id, ifconfig, DNS, routes.
//  7. Build dataChannel. Done.
//
// This is the single most complex function in the package. Most of the
// OpenVPN-specific subtleties — options-string compatibility, NCP,
// peer-id assignment — live here rather than being sprinkled across the
// codec files.
func (i *instance) runHandshake(ctx context.Context) error {
	// The control channel lives only for the handshake. Give it the
	// handshake context so that cancellation/timeout of that context
	// interrupts blocked reliable reads and writes rather than falling
	// back to the long-lived instance context.
	hsCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	i.ctrl = newControl(i.r, hsCtx)

	if err := i.ctrl.hardResetClient(hsCtx); err != nil {
		return err
	}

	tlsCfg := &tls.Config{
		RootCAs:            i.cfg.caPool,
		Certificates:       []tls.Certificate{i.cfg.clientCert},
		InsecureSkipVerify: i.cfg.skipVerify,
		MinVersion:         tls.VersionTLS12,
	}
	if i.cfg.serverCN != "" {
		tlsCfg.ServerName = i.cfg.serverCN
	}

	tlsConn := tls.Client(i.ctrl, tlsCfg)
	if err := tlsConn.HandshakeContext(hsCtx); err != nil {
		return fmt.Errorf("openvpn: TLS handshake: %w", err)
	}
	state := tlsConn.ConnectionState()

	// Pick the cipher we'll advertise in IV_CIPHERS + compare against the
	// server's PUSH_REPLY. If the config pinned one, use it; otherwise
	// advertise both and let the server pick.
	advertised := strings.Join(supportedCiphers, ":")
	preferredCipher := i.cfg.cipher
	if preferredCipher == "" {
		preferredCipher = "AES-256-GCM"
	}

	// --- Step 4: send key_method=2 ---
	kmMsg, err := buildKeyMethod2(i.cfg, advertised)
	if err != nil {
		return err
	}
	// Write via TLS — the server decrypts with its session keys and reads
	// the structured payload.
	if _, err := tlsConn.Write(kmMsg); err != nil {
		return fmt.Errorf("openvpn: write key_method=2: %w", err)
	}

	// --- Step 5: read server's key_method=2 reply ---
	if err := readKeyMethod2Reply(hsCtx, tlsConn); err != nil {
		return fmt.Errorf("openvpn: read key_method=2 reply: %w", err)
	}

	// --- Step 6: receive PUSH_REPLY ---
	// The client has to explicitly ask for PUSH_REPLY by sending
	// "PUSH_REQUEST\0" on the TLS stream. Many servers also send a
	// PUSH_REPLY proactively when auth + NCP succeed; we request
	// defensively.
	if _, err := tlsConn.Write([]byte("PUSH_REQUEST\x00")); err != nil {
		return fmt.Errorf("openvpn: send PUSH_REQUEST: %w", err)
	}
	pushReply, err := readPushReply(hsCtx, tlsConn)
	if err != nil {
		return fmt.Errorf("openvpn: read PUSH_REPLY: %w", err)
	}

	// Parse PUSH_REPLY options. Errors here usually mean the server sent
	// something we don't understand — fatal.
	parsed, err := parsePushReply(pushReply)
	if err != nil {
		return err
	}

	// Resolve cipher: server's choice trumps our preference, but must
	// still be in our supported set.
	cipher := parsed.cipher
	if cipher == "" {
		cipher = preferredCipher
	}
	cipher = strings.ToUpper(cipher)
	if !isSupportedCipher(cipher) {
		return fmt.Errorf("openvpn: server picked unsupported cipher %q", cipher)
	}

	// --- Derive keys from TLS-EKM and build the data channel ---
	km, err := deriveKeys(&state, cipher)
	if err != nil {
		return err
	}
	dc := newDataChannel(cipher, 0, km)
	dc.setPeerID(parsed.peerID)

	mtu := i.mtu
	if parsed.mtu > 0 {
		mtu = parsed.mtu
	}

	// Publish the whole session state in one locked hand-off. The UDP read
	// loop is already running and reads i.data (and, via writeToTUN, the
	// TUN once startNetstack publishes it) through the same mutex, so this
	// is the single synchronisation point between the handshake goroutine
	// and the background data plane. peerID is baked into dc before it is
	// stored, keeping the two coupled with no separate publication.
	i.mu.Lock()
	i.cipher = cipher
	i.peerID = parsed.peerID
	i.addresses = parsed.addresses
	i.dnsServers = parsed.dnsServers
	i.mtu = mtu
	i.data = dc
	i.mu.Unlock()

	// The TLS conn is no longer needed — post-handshake, OpenVPN runs
	// no traffic over it (renegotiation not in scope). Close releases
	// the crypto state but the underlying reliable continues to carry
	// ACKs for any pending CONTROL_V1s.
	_ = tlsConn.Close()
	return nil
}

// buildKeyMethod2 assembles the first client→server message on the TLS
// stream. Format (per OpenVPN source comment in ssl_pkt.c):
//
//	[4] reserved (0)
//	[1] key_method = 2
//	[32] random1
//	[48] pre_master
//	[32] random2
//	[u16 BE] options_len (includes NUL)
//	[options_str\0]
//	[u16 BE] username_len
//	[username\0] (omitted when no creds)
//	[u16 BE] password_len
//	[password\0]
//	[u16 BE] peer_info_len
//	[peer_info\0]
//
// The options string is compared byte-for-byte against the server's
// negotiated options — a mismatch aborts with "AUTH_FAILED,Options hash
// mismatch". NCP (IV_NCP, IV_CIPHERS) is carried in peer_info, NOT in
// options, so we can leave options terse.
func buildKeyMethod2(cfg *config, advertisedCiphers string) ([]byte, error) {
	var pre [48]byte
	if _, err := rand.Read(pre[:]); err != nil {
		return nil, fmt.Errorf("openvpn: rand pre_master: %w", err)
	}
	var r1 [32]byte
	if _, err := rand.Read(r1[:]); err != nil {
		return nil, fmt.Errorf("openvpn: rand r1: %w", err)
	}
	var r2 [32]byte
	if _, err := rand.Read(r2[:]); err != nil {
		return nil, fmt.Errorf("openvpn: rand r2: %w", err)
	}

	// Minimal options string. "V4" identifies the key-method-2 dialect;
	// the remaining fields must either be omitted or match the server.
	// Most modern servers don't compare strictly when tls-ekm is used,
	// but sending the expected defaults keeps us compatible with older
	// 2.5/2.6 servers.
	options := "V4,dev-type tun,link-mtu 1558,tun-mtu 1500,proto UDPv4,cipher AES-256-GCM,auth SHA256,keysize 0,key-method 2,tls-client"

	peerInfo := fmt.Sprintf(
		"IV_VER=2.6.0\n"+
			"IV_PLAT=linux\n"+
			"IV_PROTO=30\n"+ // bit flags: tls-ekm + peer-id + AEAD + push-peer-info
			"IV_CIPHERS=%s\n"+
			"IV_NCP=2\n"+
			"IV_LZO=0\n"+
			"IV_LZ4=0\n"+
			"IV_COMP_STUB=0\n"+
			"IV_COMP_STUBv2=0\n",
		advertisedCiphers,
	)

	var buf []byte
	// 4 reserved bytes + key_method
	buf = append(buf, 0, 0, 0, 0, 0x02)
	buf = append(buf, pre[:]...)
	buf = append(buf, r1[:]...)
	buf = append(buf, r2[:]...)
	buf = appendLenString(buf, options)
	buf = appendLenString(buf, cfg.username)
	buf = appendLenString(buf, cfg.password)
	buf = appendLenString(buf, peerInfo)

	return buf, nil
}

// appendLenString writes a length-prefixed string in OpenVPN's
// key-method-2 format: u16 big-endian length (including trailing NUL),
// then the string bytes, then a NUL. Empty s is encoded as length 0
// with no bytes.
func appendLenString(buf []byte, s string) []byte {
	if s == "" {
		return append(buf, 0, 0)
	}
	l := len(s) + 1 // +1 for trailing NUL
	var h [2]byte
	binary.BigEndian.PutUint16(h[:], uint16(l))
	buf = append(buf, h[:]...)
	buf = append(buf, s...)
	return append(buf, 0)
}

// readKeyMethod2Reply drains the server's key_method=2 response. We don't
// actually use its random material for key derivation (TLS-EKM replaces
// that role), so we just read enough to advance past it: the server
// response has the same framing as the client's message but without
// pre_master (server sends random1, random2, options, peer_info).
//
// For robustness against slightly different server dialects, we parse
// leniently: read the fixed-length prefix, then drain the 4
// length-prefixed strings, then stop.
func readKeyMethod2Reply(ctx context.Context, conn *tls.Conn) error {
	// Apply a deadline derived from ctx.
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(dl)
		defer conn.SetReadDeadline(time.Time{})
	}
	// Fixed header: 4 reserved + 1 key_method + 32 random1 + 32 random2 = 69
	var head [69]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return fmt.Errorf("read fixed header: %w", err)
	}
	// Then: options, username, password, peer_info (each length-prefixed).
	for _, name := range []string{"options", "username", "password", "peer_info"} {
		if _, err := readLenString(conn); err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
	}
	return nil
}

func readLenString(r io.Reader) (string, error) {
	var l [2]byte
	if _, err := io.ReadFull(r, l[:]); err != nil {
		return "", err
	}
	length := binary.BigEndian.Uint16(l[:])
	if length == 0 {
		return "", nil
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	// Trim trailing NUL if present.
	return strings.TrimRight(string(buf), "\x00"), nil
}

// readPushReply reads until it sees a PUSH_REPLY line, which arrives on
// the TLS stream as a single null-terminated message starting with
// "PUSH_REPLY,". Some servers send AUTH_FAILED or similar instead — we
// propagate those as errors so the caller gets a useful message.
func readPushReply(ctx context.Context, conn *tls.Conn) (string, error) {
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(dl)
		defer conn.SetReadDeadline(time.Time{})
	}
	// Messages on the TLS stream are \0-terminated. We read byte-at-a-time
	// until NUL; this is fine because messages are short (< 1 KiB).
	var buf []byte
	one := make([]byte, 1)
	for {
		n, err := conn.Read(one)
		if err != nil {
			return "", err
		}
		if n == 0 {
			continue
		}
		if one[0] == 0 {
			break
		}
		buf = append(buf, one[0])
	}
	msg := string(buf)
	if strings.HasPrefix(msg, "AUTH_FAILED") {
		return "", fmt.Errorf("openvpn: server rejected auth: %s", msg)
	}
	if !strings.HasPrefix(msg, "PUSH_REPLY,") {
		return "", fmt.Errorf("openvpn: expected PUSH_REPLY, got %q", truncate(msg, 120))
	}
	return strings.TrimPrefix(msg, "PUSH_REPLY,"), nil
}

type pushReplyInfo struct {
	cipher     string
	peerID     uint32
	mtu        int
	addresses  []netip.Addr
	dnsServers []netip.Addr
}

// parsePushReply extracts the fields we care about from a PUSH_REPLY.
// Format is a comma-separated list of "key value" entries:
//
//	ifconfig 10.8.0.2 10.8.0.1,route-gateway 10.8.0.1,cipher AES-256-GCM,peer-id 3,dhcp-option DNS 8.8.8.8,tun-mtu 1500
//
// We ignore entries we don't handle (route, route-gateway, ping, etc.);
// a production client would honour routes to steer specific destinations,
// but clambhook's use case is "all traffic through this tunnel" via the
// netstack's default route, so route lists aren't load-bearing.
func parsePushReply(body string) (*pushReplyInfo, error) {
	info := &pushReplyInfo{}
	for _, entry := range strings.Split(body, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Fields(entry)
		if len(parts) == 0 {
			continue
		}
		switch parts[0] {
		case "ifconfig":
			// "ifconfig <local> <peer>" — we only use <local> (interior IP).
			if len(parts) < 2 {
				return nil, errors.New("openvpn: PUSH_REPLY ifconfig missing address")
			}
			a, err := netip.ParseAddr(parts[1])
			if err != nil {
				return nil, fmt.Errorf("openvpn: PUSH_REPLY ifconfig: %w", err)
			}
			info.addresses = append(info.addresses, a)
		case "peer-id":
			if len(parts) < 2 {
				return nil, errors.New("openvpn: PUSH_REPLY peer-id missing value")
			}
			v, err := strconv.ParseUint(parts[1], 10, 32)
			if err != nil {
				return nil, fmt.Errorf("openvpn: PUSH_REPLY peer-id: %w", err)
			}
			info.peerID = uint32(v)
		case "cipher":
			if len(parts) >= 2 {
				info.cipher = parts[1]
			}
		case "tun-mtu":
			if len(parts) >= 2 {
				if v, err := strconv.Atoi(parts[1]); err == nil {
					info.mtu = v
				}
			}
		case "dhcp-option":
			// "dhcp-option DNS 8.8.8.8"
			if len(parts) >= 3 && strings.EqualFold(parts[1], "DNS") {
				if a, err := netip.ParseAddr(parts[2]); err == nil {
					info.dnsServers = append(info.dnsServers, a)
				}
			}
		}
	}
	return info, nil
}

func isSupportedCipher(c string) bool {
	for _, k := range supportedCiphers {
		if c == k {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
