package openvpn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"

	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// instance is one live OpenVPN session: a UDP socket, a reliable control
// channel, a data channel with AEAD keys, and a gVisor netstack-backed
// virtual TUN that translates L3 IP packets into the L4 net.Conn semantics
// the rest of clambhook expects. It's the moral equivalent of
// wireguard/device.go's wgInstance.
//
// Built once per server config (guarded by sync.Once in the dialer) and
// lives for the daemon's lifetime. All the per-flow work happens on the
// netstack side — inst.tnet.DialContextTCPAddrPort creates a fresh TCP
// conn for every Dial() into the tunnel.
type instance struct {
	cfg *config

	udp *net.UDPConn

	r    *reliable
	ctrl *control

	// mu owns the session state that is published *after* construction, by
	// the handshake and netstack bring-up, and then read concurrently by
	// the background data-plane goroutines. The UDP read loop is started
	// in newInstance before any of these fields are set, so it races the
	// handshake unless every read and write goes through this lock. The
	// guarded set is: data, tunDev, tnet, peerID, cipher, addresses,
	// dnsServers, mtu. Access it only through the accessors / locked blocks
	// below — never touch these fields directly from another goroutine.
	mu   sync.RWMutex
	data *dataChannel

	tunDev tun.Device
	tnet   *netstack.Net

	// Session-assigned by the server in PUSH_REPLY. The muxer goroutines
	// and data.seal need it to be set before they can emit data packets
	// the server will accept. It is copied into the dataChannel (via
	// setPeerID) before data is published, so the two stay coupled.
	peerID uint32

	// Negotiated cipher, chosen by NCP in handshake.go.
	cipher string

	// Interior interface state — decoded from the server's PUSH_REPLY
	// `ifconfig` line. Addresses pins the netstack NIC's addresses;
	// dnsServers populates the in-tunnel resolver.
	addresses  []netip.Addr
	dnsServers []netip.Addr
	mtu        int

	// Read buffer for the UDP loop. Sized for 1500-byte MTU plus slack.
	readBuf []byte

	// Lifecycle.
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

// dataChannelRef returns the currently published data channel, or nil if
// the handshake has not yet built it. Cheap enough for the per-packet hot
// path: the RLock is uncontended once the session is up.
func (i *instance) dataChannelRef() *dataChannel {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.data
}

// device returns the currently published TUN device, or nil before
// startNetstack has run (or after Close nils nothing — it only stops it).
func (i *instance) device() tun.Device {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.tunDev
}

// network returns the published netstack, or nil before startNetstack.
func (i *instance) network() *netstack.Net {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.tnet
}

// interiorAddrs returns the netstack NIC addresses pushed by the server.
func (i *instance) interiorAddrs() []netip.Addr {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.addresses
}

// newInstance runs the full bring-up sequence:
//  1. Open the UDP socket to cfg.remote.
//  2. Start the reliable-transport machinery.
//  3. Spawn a UDP read goroutine that demultiplexes into control vs. data.
//  4. Drive HARD_RESET, TLS handshake, key negotiation, PUSH_REPLY.
//  5. Bring up the netstack TUN with the ifconfig the server pushed.
//  6. Start the tunnel muxer goroutines.
//
// On any failure, it unwinds whatever it already started. The returned
// instance is fully ready: Dial() through its netstack will reach
// addresses on the far side of the VPN.
func newInstance(ctx context.Context, cfg *config) (*instance, error) {
	raddr, err := net.ResolveUDPAddr("udp", cfg.remote)
	if err != nil {
		return nil, fmt.Errorf("openvpn: resolve %s: %w", cfg.remote, err)
	}
	udp, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return nil, fmt.Errorf("openvpn: dial UDP %s: %w", cfg.remote, err)
	}

	r, err := newReliable(udp)
	if err != nil {
		udp.Close()
		return nil, err
	}

	// A fresh context used for background goroutines (UDP read loop +
	// muxers). Derived from context.Background, not from the caller's ctx,
	// because the dialer should stay up even after Dial's ctx ends.
	bgCtx, cancel := context.WithCancel(context.Background())

	inst := &instance{
		cfg:     cfg,
		udp:     udp,
		r:       r,
		mtu:     cfg.tunMTU,
		readBuf: make([]byte, 1<<16),
		ctx:     bgCtx,
		cancel:  cancel,
	}
	// The UDP read loop starts now, before the handshake has produced a
	// data channel or TUN. Until data is published it drops P_DATA_V2
	// datagrams (stray keepalives), and it reads data/tunDev through the
	// instance mutex so the handshake's later publication is race-free.
	inst.wg.Add(1)
	go inst.udpReadLoop()

	// Drive the handshake. If it fails, tear everything down and bail.
	if err := inst.runHandshake(ctx); err != nil {
		inst.Close()
		return nil, err
	}

	// Handshake published inst.cipher, inst.peerID, inst.addresses,
	// inst.data, etc. Now bring up the netstack and wire the data-plane
	// goroutines.
	if err := inst.startNetstack(); err != nil {
		inst.Close()
		return nil, fmt.Errorf("openvpn: start netstack: %w", err)
	}
	inst.startMuxers()

	return inst, nil
}

// startNetstack creates the virtual TUN + userspace TCP/IP stack. Mirrors
// the pattern in wireguard/device.go — same underlying library, same
// calling convention. It reads the interior config the handshake published
// and publishes tunDev/tnet under the same lock, since the UDP read loop
// may already be delivering data packets that reach writeToTUN.
func (i *instance) startNetstack() error {
	i.mu.RLock()
	addrs := i.addresses
	dns := i.dnsServers
	mtu := i.mtu
	i.mu.RUnlock()

	if len(addrs) == 0 {
		return errors.New("openvpn: PUSH_REPLY did not set an interior IP (ifconfig)")
	}
	if mtu <= 0 {
		mtu = 1500
	}
	tunDev, tnet, err := netstack.CreateNetTUN(addrs, dns, mtu)
	if err != nil {
		return fmt.Errorf("openvpn: create netstack tun: %w", err)
	}

	i.mu.Lock()
	i.tunDev = tunDev
	i.tnet = tnet
	i.mu.Unlock()
	return nil
}

// Close stops all background goroutines, closes the UDP socket, and
// releases the netstack. Idempotent.
func (i *instance) Close() error {
	return i.close(true)
}

func (i *instance) close(wait bool) error {
	var firstErr error
	i.closeOnce.Do(func() {
		if i.cancel != nil {
			i.cancel()
		}
		if dev := i.device(); dev != nil {
			if err := dev.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if i.r != nil {
			_ = i.r.close()
		}
		if i.udp != nil {
			if err := i.udp.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		i.closeErr = firstErr
	})
	if wait {
		i.wg.Wait()
	}
	return i.closeErr
}

func (i *instance) isClosed() bool {
	if i.ctx == nil {
		return false
	}
	select {
	case <-i.ctx.Done():
		return true
	default:
		return false
	}
}

// udpReadLoop is the single UDP reader: every datagram the kernel
// delivers goes through here first. It dispatches by opcode to either
// the reliable control layer or the data channel.
//
// Running only one goroutine on Read is important — net.UDPConn.Read is
// safe for concurrent callers but would cause packets to be reordered
// at the demux stage. Keeping it single-threaded preserves arrival order
// up to the dispatch point.
func (i *instance) udpReadLoop() {
	defer i.wg.Done()
	buf := make([]byte, 1<<16)
	for {
		// Honour cancellation: a closed UDPConn unblocks Read with an
		// error we can swallow cleanly.
		select {
		case <-i.ctx.Done():
			return
		default:
		}

		n, err := i.udp.Read(buf)
		if err != nil {
			// Expected once Close() runs; if it happens earlier, the
			// daemon will notice via read failures on the netstack side.
			return
		}
		if n < 1 {
			continue
		}
		opcode, _ := splitOpByte(buf[0])
		if opcode == OpcodeDataV2 {
			dc := i.dataChannelRef()
			if dc == nil {
				// Drop until data channel is ready — this should only
				// happen for stray keepalives during the handshake window.
				continue
			}
			// Copy before handing off; the data loop keeps the slice past
			// this iteration via its write to the TUN.
			pkt := append([]byte(nil), buf[:n]...)
			pt, err := dc.open(pkt)
			if err != nil {
				// Ignore decryption failures. A misauthenticated packet
				// shouldn't take down the tunnel — an attacker could
				// otherwise DoS us by injecting garbage. Real clients
				// count these in stats; we just drop silently.
				continue
			}
			i.writeToTUN(pt)
			continue
		}
		// Everything else is control-plane. Copy and hand off; reliable
		// takes its own copy of the payload after decoding.
		pkt := append([]byte(nil), buf[:n]...)
		if err := i.r.handleIncoming(pkt); err != nil {
			// Malformed control packets are a weak signal of protocol
			// drift or a misbehaving peer. Log once we have a logger;
			// for now, drop and continue.
			continue
		}
	}
}

// writeToTUN writes a decrypted IP packet to the netstack. tun.Device
// uses a batched write API; we build a 1-element batch. Offset 0 matches
// how wireguard-go's own netstack uses the TUN.
func (i *instance) writeToTUN(pkt []byte) {
	dev := i.device()
	if dev == nil {
		return
	}
	// The batched Write signature wants [][]byte with each element an
	// IP-packet-sized buffer, and offset where the payload starts.
	bufs := [][]byte{pkt}
	_, _ = dev.Write(bufs, 0)
}
