package openvpn

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/JohnThre/clambhook/pkg/cnet"
)

// OpenVPN starts TLS/data-channel renegotiation once a send-side packet
// counter reaches this threshold, well before uint32 wrap. This client does
// not implement soft-reset renegotiation yet, so reaching the threshold is a
// fatal data-channel condition.
const dataPacketIDRekeyThreshold uint32 = 0xFF000000

var errDataChannelRekeyRequired = errors.New("openvpn: data-channel packet ID reached rekey threshold")

// dataChannel seals and opens P_DATA_V2 packets for one direction-pair.
// It holds the per-direction keys and implicit IVs from keyMaterial, a
// monotonic send-side packet ID counter, and a replay window for the
// receive side.
//
// All state is instance-scoped: a dataChannel for a given key generation
// is discarded when renegotiation produces new keys (out of scope for
// v1, but this struct is designed to allow a clean rebuild).
type dataChannel struct {
	cipher string // "AES-256-GCM" or "CHACHA20-POLY1305"
	keyID  byte
	peerID uint32 // server-assigned, 0 until PUSH_REPLY arrives

	sendKey        []byte
	sendImplicitIV []byte
	recvKey        []byte
	recvImplicitIV []byte

	// Send-side: monotonic, starts at 1 (OpenVPN reserves 0).
	sendMu       sync.Mutex
	nextPacketID uint32

	// Receive-side replay window: a 64-bit bitmap of recently-seen packet
	// IDs. highest is the largest ID seen so far; each bit in window
	// represents (highest - i) for i in [0, 64). A packet ID is rejected
	// if it's below highest-63 or if its bit is already set.
	recvMu  sync.Mutex
	highest uint32
	window  uint64
}

func newDataChannel(cipher string, keyID byte, km *keyMaterial) *dataChannel {
	return &dataChannel{
		cipher:         cipher,
		keyID:          keyID,
		sendKey:        km.clientCipherKey,
		sendImplicitIV: km.clientImplicitIV,
		recvKey:        km.serverCipherKey,
		recvImplicitIV: km.serverImplicitIV,
		nextPacketID:   1,
	}
}

func (d *dataChannel) setPeerID(peerID uint32) {
	d.peerID = peerID
}

// seal encrypts a raw IP packet and returns the full P_DATA_V2 datagram
// ready to write to the UDP socket. The returned slice is freshly
// allocated (callers may retain it).
//
// Wire layout:
//
//	[opcode+keyid (1)] [peer_id (3)] [packet_id (4)] [tag (16)] [ciphertext (N)]
//
// AEAD binding:
//
//	nonce (12) = packet_id (4, BE) || implicit_iv (8)
//	aad  (8)   = opcode+peer_id (4) || packet_id (4)
//
// Tying the AAD to the on-wire header prevents an attacker from swapping
// the opcode/peer-id of a valid ciphertext to misroute it.
func (d *dataChannel) seal(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("openvpn: empty data-channel payload")
	}
	pid, err := d.nextSendPacketID()
	if err != nil {
		return nil, err
	}

	prefix := dataV2Prefix(d.keyID, d.peerID)

	var pidBytes [aeadPacketIDLen]byte
	binary.BigEndian.PutUint32(pidBytes[:], pid)

	// nonce = pid (BE) || implicitIV. This is equivalent to OpenVPN's
	// zero-prefixed implicit-IV XOR when the first four implicit-IV bytes
	// are zero and the next eight come from the AEAD HMAC-key slot.
	nonce := make([]byte, aeadNonceLen)
	copy(nonce[0:4], pidBytes[:])
	copy(nonce[4:], d.sendImplicitIV)

	// aad = prefix || pid (the authenticated on-wire header)
	aad := make([]byte, 8)
	copy(aad[0:4], prefix[:])
	copy(aad[4:], pidBytes[:])

	ct, tag, err := aeadSeal(d.cipher, d.sendKey, nonce, plaintext, aad)
	if err != nil {
		return nil, err
	}

	// Assemble: prefix(4) || pid(4) || tag || ct
	out := make([]byte, 0, dataV2PrefixLen+4+len(ct)+len(tag))
	out = append(out, prefix[:]...)
	out = append(out, pidBytes[:]...)
	out = append(out, tag...)
	out = append(out, ct...)
	return out, nil
}

func (d *dataChannel) nextSendPacketID() (uint32, error) {
	d.sendMu.Lock()
	defer d.sendMu.Unlock()

	if d.nextPacketID == 0 || d.nextPacketID >= dataPacketIDRekeyThreshold {
		return 0, errDataChannelRekeyRequired
	}
	pid := d.nextPacketID
	d.nextPacketID++
	return pid, nil
}

// open is the inverse of seal: validates, decrypts, returns the raw IP
// packet. Rejects replays.
func (d *dataChannel) open(packet []byte) ([]byte, error) {
	if len(packet) < dataV2PrefixLen+4+16 {
		return nil, fmt.Errorf("openvpn: P_DATA_V2 too short: %d bytes", len(packet))
	}
	// Parse but don't reuse the public parser — we want to validate that
	// the opcode is DataV2 *and* extract everything else in one pass.
	opcode, _ := splitOpByte(packet[0])
	if opcode != OpcodeDataV2 {
		return nil, fmt.Errorf("openvpn: open: expected P_DATA_V2, got opcode %d", opcode)
	}
	prefix := packet[:dataV2PrefixLen]
	pid := binary.BigEndian.Uint32(packet[dataV2PrefixLen : dataV2PrefixLen+4])
	body := packet[dataV2PrefixLen+4:]
	if len(body) < 16 {
		return nil, errors.New("openvpn: P_DATA_V2 missing AEAD tag")
	}
	if len(body) == 16 {
		return nil, errors.New("openvpn: P_DATA_V2 missing AEAD payload")
	}
	tag := body[:16]
	ct := body[16:]

	if !d.checkReplay(pid) {
		return nil, fmt.Errorf("openvpn: replay detected (packet id %d)", pid)
	}

	nonce := make([]byte, aeadNonceLen)
	binary.BigEndian.PutUint32(nonce[0:4], pid)
	copy(nonce[4:], d.recvImplicitIV)

	aad := make([]byte, 8)
	copy(aad[0:4], prefix)
	copy(aad[4:], packet[dataV2PrefixLen:dataV2PrefixLen+4])

	pt, err := aeadOpen(d.cipher, d.recvKey, nonce, ct, aad, tag)
	if err != nil {
		return nil, err
	}
	d.commitReplay(pid)
	return pt, nil
}

// checkReplay reports whether pid is acceptable. It does NOT record the
// packet — commitReplay does that on successful decrypt. This split
// matters: if decryption fails, we shouldn't advance the window, because
// an attacker could otherwise inject a high packet ID with a garbage
// AEAD tag and shift our window forward, locking out legitimate future
// packets.
func (d *dataChannel) checkReplay(pid uint32) bool {
	d.recvMu.Lock()
	defer d.recvMu.Unlock()

	if pid == 0 {
		return false
	}
	if pid > d.highest {
		return true // new packet past current window — allowed
	}
	// Packet is within the 64-id window — already seen?
	diff := d.highest - pid
	if diff >= 64 {
		return false // too old
	}
	return d.window&(1<<diff) == 0
}

func (d *dataChannel) commitReplay(pid uint32) {
	d.recvMu.Lock()
	defer d.recvMu.Unlock()

	if pid > d.highest {
		shift := pid - d.highest
		if shift >= 64 {
			d.window = 1 // only the just-arrived packet is set
		} else {
			d.window = (d.window << shift) | 1
		}
		d.highest = pid
		return
	}
	diff := d.highest - pid
	if diff < 64 {
		d.window |= 1 << diff
	}
}

// aeadSeal dispatches to the right primitive from pkg/cnet. We keep this
// indirection (rather than calling cnet directly from seal) so the data
// channel logic stays cipher-agnostic and testable with a fake AEAD.
func aeadSeal(cipher string, key, nonce, plaintext, aad []byte) (ct, tag []byte, err error) {
	switch cipher {
	case "AES-256-GCM":
		return cnet.AES256GCMEncrypt(key, nonce, plaintext, aad)
	case "CHACHA20-POLY1305":
		return cnet.ChaCha20Poly1305Encrypt(key, nonce, plaintext, aad)
	default:
		return nil, nil, fmt.Errorf("openvpn: unknown AEAD cipher %q", cipher)
	}
}

func aeadOpen(cipher string, key, nonce, ciphertext, aad, tag []byte) ([]byte, error) {
	switch cipher {
	case "AES-256-GCM":
		return cnet.AES256GCMDecrypt(key, nonce, ciphertext, aad, tag)
	case "CHACHA20-POLY1305":
		return cnet.ChaCha20Poly1305Decrypt(key, nonce, ciphertext, aad, tag)
	default:
		return nil, fmt.Errorf("openvpn: unknown AEAD cipher %q", cipher)
	}
}
