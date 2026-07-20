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
//     SOCKS5 UDP header, forwards the payload via chain.DialPacket.
//     - chainToClient: reads payloads from the chain, wraps them in a
//     SOCKS5 UDP header identifying the source peer, and writes to the
//     relay socket (addressed to the SOCKS client).
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
func (s *SOCKSv5) handleUDPAssociate(ctx context.Context, control net.Conn, ce *connEvents) {
	// The relay socket and the BND.ADDR we return must be reachable by the
	// SOCKS client on the same IP family as the TCP control connection: an
	// IPv6 client cannot reach a relay bound on the IPv4 wildcard, and an
	// IPv4 client cannot reach an IPv6-only relay. Bind the relay to the
	// control connection's family so the reported endpoint is reachable.
	relayNetwork, wildcard, bndFallback := relayBindForControl(control)
	relay, err := net.ListenUDP(relayNetwork, &net.UDPAddr{IP: wildcard, Port: 0})
	if err != nil {
		log.Printf("socks5 udp: listen relay: %v", err)
		_ = writeReply(control, repGeneralFailure, "")
		return
	}
	defer relay.Close()

	// The BND.ADDR we return must be reachable by the SOCKS client. Derive it
	// from the TCP control's local addr so loopback, NAT, and multi-homed
	// hosts resolve correctly; fall back to the loopback of the relay's family
	// when the local addr is unavailable.
	bndHost, _, err := net.SplitHostPort(control.LocalAddr().String())
	if err != nil || bndHost == "" {
		bndHost = bndFallback
	}
	bndPort := relay.LocalAddr().(*net.UDPAddr).Port
	if err := writeReply(control, repSuccess, net.JoinHostPort(bndHost, strconv.Itoa(bndPort))); err != nil {
		log.Printf("socks5 udp: write reply: %v", err)
		return
	}

	// Clear handshake deadline on the control conn; the association is now
	// long-lived, bounded only by the client staying connected.
	_ = control.SetDeadline(time.Time{})

	// Shared state: the SOCKS client's UDP source address, learned on first
	// datagram. Protected by its own mutex because two goroutines read it.
	var (
		clientMu    sync.RWMutex
		clientAddr  *net.UDPAddr
		sessionMu   sync.Mutex
		sessions    = make(map[string]net.PacketConn)
		reported    bool
		established bool
	)

	// Signalling: close this to tear down both goroutines.
	udpCtx, udpCancel := context.WithCancel(ctx)
	udpCtx = ce.attach(udpCtx)
	defer udpCancel()
	defer func() {
		sessionMu.Lock()
		defer sessionMu.Unlock()
		for _, pc := range sessions {
			_ = pc.Close()
		}
	}()

	startSessionReader := func(chainPC net.PacketConn) {
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
				if rxCounter := ce.rxCounter(); rxCounter != nil {
					rxCounter.Add(uint64(n))
				}
				if _, err := relay.WriteToUDP(framed, dst); err != nil {
					log.Printf("socks5 udp: relay write: %v", err)
					return
				}
			}
		}()
	}

	getSession := func(plan RoutePlan, target string) (net.PacketConn, error) {
		if plan.DialPacket == nil {
			return nil, errors.New("route does not support UDP")
		}
		key := plan.Action + "|" + plan.ChainName
		sessionMu.Lock()
		if pc := sessions[key]; pc != nil {
			sessionMu.Unlock()
			return pc, nil
		}
		sessionMu.Unlock()

		// Planning (including prompts) already completed on udpCtx. Give only
		// the outbound session setup its own fresh dial budget.
		dialCtx, cancelDial := context.WithTimeout(udpCtx, 30*time.Second)
		pc, err := plan.DialPacket(dialCtx, target)
		cancelDial()
		if err != nil {
			return nil, err
		}

		sessionMu.Lock()
		if existing := sessions[key]; existing != nil {
			sessionMu.Unlock()
			_ = pc.Close()
			return existing, nil
		}
		sessions[key] = pc
		sessionMu.Unlock()
		startSessionReader(pc)
		return pc, nil
	}

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
			targetAddr := net.JoinHostPort(hdr.addr, strconv.Itoa(int(hdr.port)))
			plan, err := s.plan(udpCtx, "udp", targetAddr, src.String())
			if err != nil {
				log.Printf("socks5 udp: route plan %s failed: %v", targetAddr, err)
				continue
			}
			ce.emitRuleDecision(plan)
			sessionMu.Lock()
			if !reported {
				ce.emitDialingPlan(plan)
				reported = true
			}
			sessionMu.Unlock()
			if plan.Action == RouteActionBlock || plan.Action == RouteActionReject {
				continue
			}
			chainPC, err := getSession(plan, targetAddr)
			if err != nil {
				log.Printf("socks5 udp: chain dial/write setup %s failed: %v", targetAddr, err)
				return
			}
			sessionMu.Lock()
			if !established {
				ce.emitEstablished()
				established = true
			}
			sessionMu.Unlock()
			if txCounter := ce.txCounter(); txCounter != nil {
				txCounter.Add(uint64(len(payload)))
			}
			target := &addrForWrite{host: hdr.addr, port: int(hdr.port)}
			if _, err := chainPC.WriteTo(payload, target); err != nil {
				log.Printf("socks5 udp: chain write: %v", err)
				return
			}
		}
	}()

	// Keep the TCP control conn open. RFC 1928 ties the association to this
	// connection. Listener cancellation must close it too; otherwise io.Copy
	// can pin the handler until the client eventually disconnects.
	stopCh := make(chan struct{})
	defer close(stopCh)
	go func() {
		select {
		case <-ctx.Done():
			udpCancel()
			_ = relay.Close()
			_ = control.Close()
		case <-stopCh:
		}
	}()
	_, _ = io.Copy(io.Discard, control)
	udpCancel()
}

// relayBindForControl selects the UDP relay bind parameters matching the TCP
// control connection's IP family. A UDP-associate relay bound on the wrong
// family is unreachable by the client, so an IPv6 control connection gets an
// IPv6 relay (and IPv6 loopback BND fallback); everything else defaults to
// IPv4.
func relayBindForControl(control net.Conn) (network string, wildcard net.IP, bndFallback string) {
	if ip := controlLocalIP(control); ip != nil && ip.To4() == nil && ip.To16() != nil {
		return "udp6", net.IPv6zero, "::1"
	}
	return "udp4", net.IPv4zero, "127.0.0.1"
}

// controlLocalIP returns the local IP of the control connection, or nil when
// it cannot be determined.
func controlLocalIP(control net.Conn) net.IP {
	if control == nil {
		return nil
	}
	local := control.LocalAddr()
	if local == nil {
		return nil
	}
	if tcp, ok := local.(*net.TCPAddr); ok {
		return tcp.IP
	}
	host, _, err := net.SplitHostPort(local.String())
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
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
