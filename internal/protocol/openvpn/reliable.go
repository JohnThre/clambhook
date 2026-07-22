package openvpn

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// reliable implements OpenVPN's reliability primitives on top of a raw UDP
// connection. It's only responsible for the *control* channel: data
// packets (P_DATA_V2) bypass this layer and are encrypted/decrypted
// separately in data.go.
//
// Responsibilities:
//   - Assign local packet IDs, track outstanding sends, retransmit on timeout.
//   - Buffer incoming control packets, deliver them in order to recv().
//   - Piggyback received-packet ACKs onto outgoing packets, or flush as
//     standalone P_ACK_V1 frames when nothing else is queued.
//
// Not responsible for:
//   - What's in the payload (TLS fragments live in control.go).
//   - Dispatching data packets (the caller's read loop demuxes before
//     handing control bytes to handleIncoming).
//   - Renegotiation / soft-resets (out of scope for v1).
type reliable struct {
	conn       *net.UDPConn
	localSID   sessionID
	remoteSID  sessionID
	haveRemote bool
	keyID      byte

	// Outbound sequencing.
	wmu          sync.Mutex
	writeMu      sync.Mutex               // serialises control-plane writes and write-deadline changes
	nextPacketID packetID
	pending      map[packetID]*outstanding // sent, awaiting ACK
	pendingAcks  []packetID                // incoming packet IDs we owe peer an ACK for

	// Inbound delivery — ordered, reliable byte stream of control payloads.
	rmu          sync.Mutex
	nextExpected packetID
	reorder      map[packetID]*controlPacket
	deliver      chan *controlPacket // buffered; consumer pulls via recv()

	// Lifecycle.
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// outstanding tracks a single control packet awaiting ACK. We remember the
// serialised bytes so retransmits don't re-encode (and so we don't need
// to reconstruct a freshly-piggybacked ACK list each time — but see the
// note in retransmit() about that trade-off).
type outstanding struct {
	pid     packetID
	bytes   []byte
	sentAt  time.Time
	retries int
}

// Retransmit parameters. OpenVPN's reference client uses --hand-window
// (default 60s) for the total handshake budget and --tls-timeout (default
// 2s) for the first retry. We start at 1s to favour snappy recovery on
// good networks, double to 16s max, and give up after 10 retries (which
// works out to ~60s elapsed under exponential backoff).
const (
	initialRetryInterval = 1 * time.Second
	maxRetryInterval     = 16 * time.Second
	maxRetries           = 10
)

// maxPiggybackAcks caps the ACK array on outgoing packets. OpenVPN's
// reference implementation accepts up to 255 but typically sends no more
// than 4–8; we pick 4 as a reasonable middle ground.
const maxPiggybackAcks = 4

// reorderWindow limits how many out-of-order packets we'll buffer. Each
// consumes ~1.5KB of memory; 64 frames is enough to ride out any
// realistic UDP reordering and bounds memory use if a peer misbehaves.
const reorderWindow = 64

func newReliable(conn *net.UDPConn) (*reliable, error) {
	var sid sessionID
	if _, err := rand.Read(sid[:]); err != nil {
		return nil, fmt.Errorf("openvpn: generate session id: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &reliable{
		conn:     conn,
		localSID: sid,
		pending:  make(map[packetID]*outstanding),
		reorder:  make(map[packetID]*controlPacket),
		deliver:  make(chan *controlPacket, reorderWindow),
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	// OpenVPN upstream src/openvpn/reliable.c initializes a struct reliable
	// with CLEAR(*rel), zeroing rel->packet_id, and
	// reliable_mark_active_outgoing assigns e->packet_id = rel->packet_id++.
	// The reliable control channel is therefore 0-based per direction:
	// the first HARD_RESET_CLIENT_V2 / HARD_RESET_SERVER_V2 each carry
	// packet-id 0. Starting at 1 would drop the server's id-0 reset in
	// the pkt.packetID < r.nextExpected duplicate check.
	r.nextPacketID = 0
	r.nextExpected = 0
	go r.retransmitLoop()
	return r, nil
}

// send assembles and transmits a single control packet with payload. It
// blocks until the packet is *sent* (not ACKed). Retransmission on ACK
// timeout happens in the background; the retransmit loop will call
// sendCtx.Err() if the caller-facing context has been cancelled.
//
// Safe for concurrent callers: the outbound queue is mutex-guarded and
// packet IDs are monotonic. In practice only one goroutine calls send()
// at a time (control.go is sequential), but the lock is cheap.
func (r *reliable) send(ctx context.Context, opcode byte, payload []byte) error {
	r.wmu.Lock()
	pid := r.nextPacketID
	r.nextPacketID++

	pkt := &controlPacket{
		opcode:         opcode,
		keyID:          r.keyID,
		localSessionID: r.localSID,
		packetID:       pid,
		payload:        payload,
	}
	// Piggyback up to maxPiggybackAcks pending ACKs so the peer can retire
	// its own outstanding sends. Without this the peer keeps retransmitting
	// packets we already received, wasting bandwidth and slowing the
	// handshake.
	n := len(r.pendingAcks)
	if n > maxPiggybackAcks {
		n = maxPiggybackAcks
	}
	if n > 0 {
		pkt.ackedIDs = append(pkt.ackedIDs, r.pendingAcks[:n]...)
		r.pendingAcks = r.pendingAcks[n:]
		pkt.remoteSessionID = r.remoteSID
	}

	buf, err := encodeControl(pkt)
	if err != nil {
		r.wmu.Unlock()
		return fmt.Errorf("openvpn: encode control: %w", err)
	}
	r.pending[pid] = &outstanding{pid: pid, bytes: buf, sentAt: time.Now()}
	r.wmu.Unlock()

	if err := r.writeControl(ctx, buf); err != nil {
		return fmt.Errorf("openvpn: write control: %w", err)
	}
	return nil
}

// sendAck emits a standalone P_ACK_V1 if we have pending ACKs and nothing
// else queued to piggyback them on. Called after an idle stretch so the
// peer doesn't spin on a retransmit waiting for our ACK.
func (r *reliable) sendAck() error {
	r.wmu.Lock()
	if !r.haveRemote || len(r.pendingAcks) == 0 {
		r.wmu.Unlock()
		return nil
	}
	n := len(r.pendingAcks)
	if n > maxPiggybackAcks {
		n = maxPiggybackAcks
	}
	pkt := &controlPacket{
		opcode:          OpcodeAckV1,
		keyID:           r.keyID,
		localSessionID:  r.localSID,
		ackedIDs:        append([]packetID(nil), r.pendingAcks[:n]...),
		remoteSessionID: r.remoteSID,
	}
	r.pendingAcks = r.pendingAcks[n:]
	r.wmu.Unlock()

	buf, err := encodeControl(pkt)
	if err != nil {
		return fmt.Errorf("openvpn: encode ack: %w", err)
	}
	if err := r.writeControl(context.Background(), buf); err != nil {
		return fmt.Errorf("openvpn: write ack: %w", err)
	}
	return nil
}

// recv blocks until the next in-order control packet is available, or
// ctx expires, or the reliable layer is closed.
func (r *reliable) recv(ctx context.Context) (*controlPacket, error) {
	select {
	case p, ok := <-r.deliver:
		if !ok {
			return nil, io.EOF
		}
		return p, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.done:
		return nil, io.EOF
	}
}

// handleIncoming is the entry point for raw control packet bytes read off
// the UDP socket by the outer demux goroutine. It retires ACKed sends,
// queues the packet for in-order delivery, and updates remote session id.
func (r *reliable) handleIncoming(data []byte) error {
	pkt, err := decodeControl(data)
	if err != nil {
		return err
	}

	// Clip-copy payload so the caller can reuse its UDP read buffer.
	if pkt.payload != nil {
		pkt.payload = append([]byte(nil), pkt.payload...)
	}

	// Retire ACKed outbound packets.
	if len(pkt.ackedIDs) > 0 {
		r.wmu.Lock()
		for _, acked := range pkt.ackedIDs {
			delete(r.pending, acked)
		}
		r.wmu.Unlock()
	}

	// Learn the remote session id from either a HARD_RESET_SERVER_V2 or
	// any packet that sets it explicitly via ACKs. HARD_RESET_SERVER_V2
	// always carries an ACK of our HARD_RESET_CLIENT_V2, so remoteSID
	// comes through the ackedIDs path; we set it from localSessionID
	// regardless to be robust.
	r.wmu.Lock()
	if !r.haveRemote {
		r.remoteSID = pkt.localSessionID
		r.haveRemote = true
	}
	r.wmu.Unlock()

	// Pure ACK frames have no packet ID; they exist only to retire sends.
	if pkt.isAck() {
		return nil
	}

	// Record the packet ID so we ACK it back in the next outbound packet
	// (or in a standalone ACK if the channel goes idle).
	r.wmu.Lock()
	r.pendingAcks = append(r.pendingAcks, pkt.packetID)
	r.wmu.Unlock()

	// In-order delivery: stash until its turn, then drain any queued.
	r.rmu.Lock()
	defer r.rmu.Unlock()

	if pkt.packetID < r.nextExpected {
		// Duplicate — peer retransmitted a packet whose ACK it didn't see
		// in time. Don't re-deliver; the ACK we just queued is what they
		// need.
		return nil
	}
	r.reorder[pkt.packetID] = pkt
	for {
		next, ok := r.reorder[r.nextExpected]
		if !ok {
			break
		}
		delete(r.reorder, r.nextExpected)
		select {
		case r.deliver <- next:
		case <-r.done:
			return nil
		}
		r.nextExpected++
	}
	return nil
}

// retransmitLoop periodically rewrites outstanding packets whose ACKs
// have not arrived. Runs until close(). Uses a 250ms tick for resolution
// — finer-grained than needed but cheap, and it means the effective
// retransmit interval is within 250ms of the target.
func (r *reliable) retransmitLoop() {
	defer close(r.done)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-tick.C:
			if err := r.retransmit(); err != nil {
				r.closeWith(err)
				return
			}
		}
	}
}

func (r *reliable) retransmit() error {
	now := time.Now()
	var dead []packetID
	var toSend [][]byte

	r.wmu.Lock()
	for pid, o := range r.pending {
		interval := initialRetryInterval << o.retries
		if interval > maxRetryInterval {
			interval = maxRetryInterval
		}
		if now.Sub(o.sentAt) < interval {
			continue
		}
		if o.retries >= maxRetries {
			dead = append(dead, pid)
			continue
		}
		o.sentAt = now
		o.retries++
		// Re-send the originally serialised bytes. This means a retransmit
		// carries the *same* ACK piggyback as the original — not the
		// freshest set — but that's fine: peers handle duplicate ACKs
		// gracefully, and re-encoding risks reordering packet IDs when
		// concurrent sends occur.
		toSend = append(toSend, o.bytes)
	}
	r.wmu.Unlock()

	if len(dead) > 0 {
		return fmt.Errorf("openvpn: control packets %v exceeded %d retries (handshake stalled)", dead, maxRetries)
	}

	for _, buf := range toSend {
		if err := r.writeControl(context.Background(), buf); err != nil {
			return fmt.Errorf("openvpn: retransmit write: %w", err)
		}
	}
	return nil
}

// writeControl performs a single control-plane UDP write under a mutex so
// that deadline changes made for one caller (e.g. a context deadline during
// the handshake) do not race with other control-plane writers. The data
// channel bypasses this path and writes the same UDPConn directly, which is
// safe because control-plane deadlines are never left in place after send
// returns.
func (r *reliable) writeControl(ctx context.Context, buf []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	if dl, ok := ctx.Deadline(); ok {
		_ = r.conn.SetWriteDeadline(dl)
		defer r.conn.SetWriteDeadline(time.Time{})
	}
	if _, err := r.conn.Write(buf); err != nil {
		return err
	}
	return nil
}

// close tears down the reliable layer. Idempotent. Safe to call from any
// goroutine.
func (r *reliable) close() error {
	r.closeOnce.Do(func() {
		r.cancel()
	})
	<-r.done
	return r.closeErr
}

func (r *reliable) closeWith(err error) {
	r.closeOnce.Do(func() {
		r.closeErr = err
		r.cancel()
	})
}
