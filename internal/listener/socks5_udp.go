package listener

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"
)

// handleUDPAssociate implements RFC 1928 §6 UDP ASSOCIATE.
//
// Flow:
//  1. Open a local UDP socket (the "relay" socket the SOCKS client will
//     send datagrams to). Bind to the same IP family as the control conn;
//     port is assigned by the kernel.
//  2. Open a UDP-carrying session through the chain.
//  3. Reply to the SOCKS client over the TCP control connection with the
//     relay socket's BND address.
//  4. Run two goroutines:
//     - clientToChain: reads datagrams from the relay socket, parses the
//       SOCKS5 UDP header, forwards the payload via chain.DialPacket.
//     - chainToClient: reads payloads from the chain, wraps them in a
//       SOCKS5 UDP header identifying the source peer, and writes to the
//       relay socket (addressed to the SOCKS client).
//  5. The association lives for as long as the TCP control connection is
//     open. When the client closes the TCP control, both goroutines exit
//     and the relay socket is closed.
//
// Notes:
//   - The SOCKS5 client's source addr is locked to the first sender we see
//     on the relay socket. RFC 1928 allows the client to pre-declare it in
//     the UDP ASSOCIATE request, but in practice most clients send 0.0.0.0:0
//     and let the server latch on the first datagram.
//   - FRAG is rejected in the codec; almost nothing uses it.
//   - The chain.DialPacket returns a single session that multiplexes many
//     target peers in its frames, which matches our needs — we don't need
//     a per-target session.
func (s *SOCKSv5) handleUDPAssociate(ctx context.Context, control net.Conn) {
	// Local UDP relay socket — bind to any available port on a wildcard
	// address so either IPv4 or IPv6 clients can reach it.
	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		log.Printf("socks5 udp: listen relay: %v", err)
		_ = writeReply(control, repGeneralFailure, "")
		return
	}
	defer relay.Close()

	// The BND.ADDR we return must be reachable by the SOCKS client. For a
	// localhost-bound listener (our default) we can send back 127.0.0.1;
	// when bound on a real interface, derive from the TCP control's local
	// addr so NAT / multi-homed hosts resolve correctly.
	bndHost, _, _ := net.SplitHostPort(control.LocalAddr().String())
	if bndHost == "" {
		bndHost = "127.0.0.1"
	}
	bndPort := relay.LocalAddr().(*net.UDPAddr).Port
	if err := writeReply(control, repSuccess, net.JoinHostPort(bndHost, strconv.Itoa(bndPort))); err != nil {
		log.Printf("socks5 udp: write reply: %v", err)
		return
	}

	// Open the chain-side packet session.
	chainPC, err := s.dialPacket(ctx, "")
	if err != nil {
		log.Printf("socks5 udp: chain dial: %v", err)
		// The control conn already got a success reply before we tried to
		// dial; the client will notice the association is dead when the
		// control conn closes below.
		return
	}
	defer chainPC.Close()

	// Clear handshake deadline on the control conn; the association is now
	// long-lived, bounded only by the client staying connected.
	_ = control.SetDeadline(time.Time{})

	// Shared state: the SOCKS client's UDP source address, learned on first
	// datagram. Protected by its own mutex because two goroutines read it.
	var (
		clientMu   sync.RWMutex
		clientAddr *net.UDPAddr
	)

	// Signalling: close this to tear down both goroutines.
	udpCtx, udpCancel := context.WithCancel(ctx)
	defer udpCancel()

	// Goroutine 1: SOCKS client (UDP) → chain.
	go func() {
		defer udpCancel()
		buf := make([]byte, 65535)
		for {
			if err := relay.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
				return
			}
			n, src, err := relay.ReadFromUDP(buf)
			if err != nil {
				if udpCtx.Err() != nil {
					return
				}
				if errors.Is(err, net.ErrClosed) {
					return
				}
				// SetReadDeadline-induced timeout: just check ctx and loop.
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				log.Printf("socks5 udp: relay read: %v", err)
				return
			}

			// Latch the client address on first datagram.
			clientMu.Lock()
			if clientAddr == nil {
				clientAddr = src
				log.Printf("socks5 udp: association latched on %s", src)
			} else if !src.IP.Equal(clientAddr.IP) || src.Port != clientAddr.Port {
				// Drop datagrams from other peers — they're either a
				// spoof attempt or a misconfigured client.
				clientMu.Unlock()
				continue
			}
			clientMu.Unlock()

			hdr, payload, err := parseUDPDatagram(buf[:n])
			if err != nil {
				log.Printf("socks5 udp: parse datagram: %v", err)
				continue
			}
			target := &addrForWrite{host: hdr.addr, port: int(hdr.port)}
			if _, err := chainPC.WriteTo(payload, target); err != nil {
				log.Printf("socks5 udp: chain write: %v", err)
				return
			}
		}
	}()

	// Goroutine 2: chain → SOCKS client (UDP).
	go func() {
		defer udpCancel()
		buf := make([]byte, 65535)
		for {
			if err := chainPC.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
				return
			}
			n, from, err := chainPC.ReadFrom(buf)
			if err != nil {
				if udpCtx.Err() != nil {
					return
				}
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				log.Printf("socks5 udp: chain read: %v", err)
				return
			}

			clientMu.RLock()
			dst := clientAddr
			clientMu.RUnlock()
			if dst == nil {
				// We received a reply before the client sent anything — drop.
				continue
			}

			host, portStr, err := net.SplitHostPort(from.String())
			if err != nil {
				log.Printf("socks5 udp: parse source addr %q: %v", from, err)
				continue
			}
			port, _ := strconv.Atoi(portStr)
			framed, err := encodeUDPDatagram(host, uint16(port), buf[:n])
			if err != nil {
				log.Printf("socks5 udp: encode reply: %v", err)
				continue
			}
			if _, err := relay.WriteToUDP(framed, dst); err != nil {
				log.Printf("socks5 udp: relay write: %v", err)
				return
			}
		}
	}()

	// Keep the TCP control conn open. RFC 1928: the UDP association is
	// tied to the TCP control — when the client closes the control, we
	// tear everything down.
	_, _ = io.Copy(io.Discard, control)
	udpCancel()
}

// addrForWrite is the net.Addr passed to PacketConn.WriteTo when forwarding
// to the chain. The protocol-layer implementation (e.g. trojan) reads
// addr.String() and encodes it per-frame.
type addrForWrite struct {
	host string
	port int
}

func (a *addrForWrite) Network() string { return "udp" }
func (a *addrForWrite) String() string {
	return net.JoinHostPort(a.host, strconv.Itoa(a.port))
}
