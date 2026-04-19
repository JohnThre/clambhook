package openvpn

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// OpenVPN packet opcodes. These are the upper 5 bits of the first byte of
// every OpenVPN datagram; the lower 3 bits are the key_id (data-channel
// generation). We only care about the "V2" variants — V1 hard-resets are
// legacy, V3 is for tls-crypt-v2 which we don't support.
const (
	OpcodeControlHardResetClientV2 byte = 7
	OpcodeControlHardResetServerV2 byte = 8
	OpcodeControlV1                byte = 4
	OpcodeAckV1                    byte = 5
	OpcodeDataV2                   byte = 9
)

const (
	// Length of an OpenVPN session ID (RFC-style: 64 bits of randomness).
	// One per endpoint, stable for the lifetime of a TLS session.
	sessionIDLen = 8

	// AEAD nonce layout: [packet_id (4, BE)] || [implicit_iv (8)]. The
	// implicit IV is derived from the HMAC-slot key material per side.
	aeadNonceLen       = 12
	aeadPacketIDLen    = 4
	aeadImplicitIVLen  = 8

	// P_DATA_V2 prepends a 3-byte peer-id to everything; the first byte of
	// the datagram is op+keyid, so the opcode+peer-id form a 4-byte prefix.
	dataV2PrefixLen = 4

	peerIDLen = 3
)

// sessionID is a random 8-byte identifier OpenVPN embeds in every reliable
// control packet. Client and server each pick one at HARD_RESET time;
// they don't ever change mid-connection.
type sessionID [sessionIDLen]byte

// packetID is the 32-bit monotonically increasing counter OpenVPN uses for
// reliable-layer sequencing (control channel) and AEAD nonce construction
// (data channel). Wire format is big-endian.
type packetID uint32

// opByte packs opcode and key_id into a single byte. opcode occupies the
// upper 5 bits; key_id the lower 3. Both share the byte on every OpenVPN
// packet regardless of type, which is how the receiver dispatches.
func opByte(opcode, keyID byte) byte {
	return (opcode << 3) | (keyID & 0x07)
}

func splitOpByte(b byte) (opcode, keyID byte) {
	return b >> 3, b & 0x07
}

// controlPacket is the wire form of every P_CONTROL_* and P_ACK_V1. For
// P_ACK_V1, the packetID field is omitted and payload is empty (ACKs are
// pure acknowledgement frames).
//
// Field order on the wire:
//
//	opByte (1)                              — opcode + key_id
//	localSessionID (8)
//	ackCount (1)
//	ackedIDs (4 * ackCount)                 — packet IDs being ACKed
//	remoteSessionID (8)                     — only if ackCount > 0
//	packetID (4)                            — only for non-ACK packets
//	payload (variable)                      — only for non-ACK packets
//
// Clients of this codec are the reliable transport and the control
// channel: transport assembles/parses packets and tracks ACKs; control
// ferries TLS record fragments in the payload of P_CONTROL_V1.
type controlPacket struct {
	opcode          byte
	keyID           byte
	localSessionID  sessionID
	ackedIDs        []packetID
	remoteSessionID sessionID // only valid when len(ackedIDs) > 0
	packetID        packetID  // only valid for non-ACK packets
	payload         []byte    // only valid for non-ACK packets
}

// isAck reports whether this is a pure ACK frame. P_ACK_V1 is the only
// OpenVPN packet type that omits the trailing packet ID; every other
// control packet carries one even when the payload is empty (e.g. a
// HARD_RESET has no payload but still has a packet ID).
func (p *controlPacket) isAck() bool {
	return p.opcode == OpcodeAckV1
}

// encodeControl serialises a controlPacket to wire bytes. Returns an
// error only for argument problems (too many ACKs); the buffer is
// allocated fresh, we don't share state with the caller.
func encodeControl(p *controlPacket) ([]byte, error) {
	if len(p.ackedIDs) > 255 {
		return nil, fmt.Errorf("openvpn: too many ACKs in one packet: %d", len(p.ackedIDs))
	}

	// Conservative size: fixed prefix + ACK array + optional remote
	// session id + optional packet id + payload.
	size := 1 + sessionIDLen + 1 + 4*len(p.ackedIDs)
	if len(p.ackedIDs) > 0 {
		size += sessionIDLen
	}
	if !p.isAck() {
		size += 4 + len(p.payload)
	}
	buf := make([]byte, 0, size)

	buf = append(buf, opByte(p.opcode, p.keyID))
	buf = append(buf, p.localSessionID[:]...)
	buf = append(buf, byte(len(p.ackedIDs)))
	for _, id := range p.ackedIDs {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(id))
		buf = append(buf, b[:]...)
	}
	if len(p.ackedIDs) > 0 {
		buf = append(buf, p.remoteSessionID[:]...)
	}
	if !p.isAck() {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(p.packetID))
		buf = append(buf, b[:]...)
		buf = append(buf, p.payload...)
	}
	return buf, nil
}

// decodeControl is the inverse of encodeControl. It validates the opcode
// and returns the parsed packet plus a non-nil error on any malformed
// wire data. The payload slice is a sub-slice of the input — callers that
// need to retain it past the next packet read should copy it.
func decodeControl(data []byte) (*controlPacket, error) {
	if len(data) < 1 {
		return nil, errors.New("openvpn: empty packet")
	}
	opcode, keyID := splitOpByte(data[0])
	switch opcode {
	case OpcodeControlHardResetClientV2, OpcodeControlHardResetServerV2,
		OpcodeControlV1, OpcodeAckV1:
		// ok
	default:
		return nil, fmt.Errorf("openvpn: not a control-family opcode: %d", opcode)
	}

	r := &packetReader{buf: data[1:]}
	p := &controlPacket{opcode: opcode, keyID: keyID}

	if err := r.read(p.localSessionID[:]); err != nil {
		return nil, fmt.Errorf("openvpn: read local session: %w", err)
	}
	ackCount, err := r.readByte()
	if err != nil {
		return nil, fmt.Errorf("openvpn: read ack count: %w", err)
	}
	for i := byte(0); i < ackCount; i++ {
		v, err := r.readUint32()
		if err != nil {
			return nil, fmt.Errorf("openvpn: read ack[%d]: %w", i, err)
		}
		p.ackedIDs = append(p.ackedIDs, packetID(v))
	}
	if ackCount > 0 {
		if err := r.read(p.remoteSessionID[:]); err != nil {
			return nil, fmt.Errorf("openvpn: read remote session: %w", err)
		}
	}
	if opcode != OpcodeAckV1 {
		pid, err := r.readUint32()
		if err != nil {
			return nil, fmt.Errorf("openvpn: read packet id: %w", err)
		}
		p.packetID = packetID(pid)
		p.payload = r.remaining()
	} else if r.len() != 0 {
		return nil, fmt.Errorf("openvpn: P_ACK_V1 has %d trailing bytes", r.len())
	}

	return p, nil
}

// packetReader is a byte-oriented cursor over a single UDP datagram. It
// avoids the allocation overhead of bytes.Reader for small reads (the
// fixed-size session ID and packet ID fields) without sacrificing clarity.
type packetReader struct {
	buf []byte
	off int
}

func (r *packetReader) len() int { return len(r.buf) - r.off }

func (r *packetReader) read(into []byte) error {
	n := len(into)
	if r.len() < n {
		return io.ErrUnexpectedEOF
	}
	copy(into, r.buf[r.off:r.off+n])
	r.off += n
	return nil
}

func (r *packetReader) readByte() (byte, error) {
	if r.len() < 1 {
		return 0, io.ErrUnexpectedEOF
	}
	b := r.buf[r.off]
	r.off++
	return b, nil
}

func (r *packetReader) readUint32() (uint32, error) {
	if r.len() < 4 {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.BigEndian.Uint32(r.buf[r.off:])
	r.off += 4
	return v, nil
}

func (r *packetReader) remaining() []byte {
	return r.buf[r.off:]
}

// dataV2Prefix encodes the 4-byte prefix of a P_DATA_V2 datagram: op byte
// followed by a 24-bit peer-id. Peer-id is assigned by the server in the
// PUSH_REPLY (via `peer-id` key); until we receive it we send 0, which
// OpenVPN servers accept for the first key-negotiation packet.
func dataV2Prefix(keyID byte, peerID uint32) [dataV2PrefixLen]byte {
	var out [dataV2PrefixLen]byte
	out[0] = opByte(OpcodeDataV2, keyID)
	// Only 24 bits of peerID are wire-significant; upper 8 bits of the
	// uint32 are silently dropped here. Peer-id realistically tops out at
	// ~16M, so overflow is not a real concern, but we mask just in case.
	out[1] = byte((peerID >> 16) & 0xFF)
	out[2] = byte((peerID >> 8) & 0xFF)
	out[3] = byte(peerID & 0xFF)
	return out
}

// parseDataV2Prefix reads the 4-byte op||peer-id prefix from a P_DATA_V2
// datagram. Returns the key_id and 24-bit peer-id.
func parseDataV2Prefix(b []byte) (keyID byte, peerID uint32, err error) {
	if len(b) < dataV2PrefixLen {
		return 0, 0, io.ErrUnexpectedEOF
	}
	op, k := splitOpByte(b[0])
	if op != OpcodeDataV2 {
		return 0, 0, fmt.Errorf("openvpn: not P_DATA_V2: opcode %d", op)
	}
	peerID = uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	return k, peerID, nil
}
