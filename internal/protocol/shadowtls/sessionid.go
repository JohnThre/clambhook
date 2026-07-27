package shadowtls

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// ShadowTLS v3 requires the client's TLS ClientHello to carry an HMAC-signed
// legacy_session_id: 28 random bytes followed by 4 bytes of
// HMAC-SHA1(password, ClientHello-with-those-4-bytes-zeroed).
//
// In TLS 1.3 the session_id is folded into the handshake transcript hash, so it
// cannot be patched on the wire after crypto/tls marshals the ClientHello —
// doing so desyncs the transcript and fails with "bad record MAC". The
// reference implementation forks crypto/tls to inject the id before marshaling.
//
// We stay on stdlib crypto/tls with a deterministic two-pass trick:
//
//  1. Pass 1 runs a throwaway handshake with a deterministic, self-recording
//     Rand. We capture the exact ClientHello crypto/tls produced and the random
//     byte stream it consumed. From the ClientHello we compute the HMAC.
//  2. Pass 2 runs the real handshake with the *same* Rand stream, except the 4
//     bytes that become the session_id tail are overridden with the HMAC. Every
//     other ClientHello byte is bit-identical, so crypto/tls itself marshals and
//     transcript-hashes a ClientHello whose session_id already ends in the HMAC.
//
// Two stdlib behaviours must be tamed for the passes to match byte-for-byte:
//
//   - crypto/tls only honours tls.Config.Rand for ephemeral key generation when
//     GODEBUG cryptocustomrand=1 is set. That is pinned module-wide in go.mod.
//   - crypto/internal/randutil.MaybeReadByte issues a single 1-byte read from
//     the reader (with 50% probability, from an uncontrollable global RNG)
//     before key generation, specifically to defeat rand-stream determinism.
//     With X25519 forced (see forcedCurves) the ONLY 1-byte reads crypto/tls
//     performs are these probes, so replayRand serves 1-byte reads from a
//     constant side value that never advances the main deterministic stream.
//     Both passes therefore observe an identical main stream regardless of how
//     many probes fire.
//
// Offsets within the ClientHello *handshake message* (no 5-byte record header):
//
//	msg_type(1) length(3) legacy_version(2) random(32) session_id_len(1) session_id(32) ...
//
// so session_id starts at 39 and its 4-byte HMAC tail occupies [67:71).
const (
	chSessionIDStart = 1 + 3 + 2 + tlsRandomSize + 1 // 39
	chSessionIDTail  = chSessionIDStart + tlsSessionIDSize - hmacSize
)

// tlsConfigFunc builds a *tls.Config; kept as a closure so the dialer supplies
// ServerName/ALPN/verification while this file owns the deterministic Rand.
type tlsConfigFunc func(randReader io.Reader) *tls.Config

// prepareSignedHandshake returns a hijackConn over raw plus the pass-2 Rand to
// install on the real handshake's tls.Config. The pass-2 ClientHello, produced
// by crypto/tls itself, will carry the HMAC-signed session id.
func prepareSignedHandshake(ctx context.Context, raw net.Conn, password string, build tlsConfigFunc) (*hijackConn, io.Reader, error) {
	var seed [32]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, nil, fmt.Errorf("shadowtls: seed rand: %w", err)
	}

	// Pass 1: capture the ClientHello and the consumed random stream.
	r1 := newReplayRand(seed, nil)
	ch, err := captureClientHello(ctx, build, r1)
	if err != nil {
		return nil, nil, err
	}
	if len(ch) < chSessionIDStart+tlsSessionIDSize {
		return nil, nil, &protocolError{"captured client hello too short"}
	}

	sessionID := ch[chSessionIDStart : chSessionIDStart+tlsSessionIDSize]
	mac := signature(password, ch)

	// Locate the session_id draw within the recorded stream and override its
	// 4-byte tail with the HMAC for pass 2.
	off := bytes.LastIndex(r1.served, sessionID)
	if off < 0 {
		return nil, nil, &protocolError{"session id not found in random stream"}
	}
	overrides := map[int]byte{}
	for i := 0; i < hmacSize; i++ {
		overrides[off+tlsSessionIDSize-hmacSize+i] = mac[i]
	}

	r2 := newReplayRand(seed, overrides)
	return newHijackConn(raw, password), r2, nil
}

// signature computes HMAC-SHA1(password, msg with session_id tail zeroed)[:4],
// matching the ShadowTLS v3 server's verifyClientHello.
func signature(password string, msg []byte) []byte {
	h := hmac.New(sha1.New, []byte(password))
	h.Write(msg[:chSessionIDTail])
	h.Write([]byte{0, 0, 0, 0})
	h.Write(msg[chSessionIDTail+hmacSize:])
	return h.Sum(nil)[:hmacSize]
}

// captureClientHello runs a TLS handshake far enough to emit the ClientHello,
// then aborts. It returns the ClientHello handshake message (record body,
// without the 5-byte TLS record header).
func captureClientHello(ctx context.Context, build tlsConfigFunc, r *replayRand) ([]byte, error) {
	cap := &clientHelloCapture{}
	tc := tls.Client(cap, build(r))
	// The handshake fails once the capture conn refuses to feed a ServerHello;
	// that error is expected and ignored — we only want the ClientHello bytes.
	_ = tc.HandshakeContext(ctx)

	rec := cap.buf.Bytes()
	if len(rec) < tlsHeaderSize || rec[0] != recordHandshake {
		return nil, &protocolError{"did not capture a client hello record"}
	}
	length := int(binary.BigEndian.Uint16(rec[3:tlsHeaderSize]))
	if len(rec) < tlsHeaderSize+length {
		return nil, &protocolError{"incomplete client hello record"}
	}
	return rec[tlsHeaderSize : tlsHeaderSize+length], nil
}

// clientHelloCapture is a net.Conn that records everything crypto/tls writes
// (the ClientHello) and aborts the handshake on the first Read.
type clientHelloCapture struct {
	buf bytes.Buffer
}

func (c *clientHelloCapture) Write(p []byte) (int, error)      { return c.buf.Write(p) }
func (c *clientHelloCapture) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *clientHelloCapture) Close() error                     { return nil }
func (c *clientHelloCapture) LocalAddr() net.Addr              { return dummyAddr{} }
func (c *clientHelloCapture) RemoteAddr() net.Addr             { return dummyAddr{} }
func (c *clientHelloCapture) SetDeadline(time.Time) error      { return nil }
func (c *clientHelloCapture) SetReadDeadline(time.Time) error  { return nil }
func (c *clientHelloCapture) SetWriteDeadline(time.Time) error { return nil }

// replayRand is a deterministic, self-recording io.Reader. Byte position i is
// derived from SHA-256(seed || u64le(blockIndex)); overrides replace individual
// absolute positions (used to splice the HMAC into the session_id tail on the
// second pass). Because it is fully determined by the seed, two passes consume
// an identical stream — the prerequisite for the transcript to stay in sync.
type replayRand struct {
	seed      [32]byte
	pos       int
	block     []byte
	blockIdx  uint64
	served    []byte
	overrides map[int]byte
}

func newReplayRand(seed [32]byte, overrides map[int]byte) *replayRand {
	return &replayRand{seed: seed, overrides: overrides}
}

func (r *replayRand) Read(p []byte) (int, error) {
	// randutil.MaybeReadByte probes with a single 1-byte read that fires with
	// 50% probability from an uncontrollable global RNG. Serving it from the
	// main stream would shift every subsequent byte and desync the two passes,
	// so answer 1-byte reads from a fixed side value instead. With X25519 forced
	// this is the only place crypto/tls issues a 1-byte read.
	if len(p) == 1 {
		p[0] = 0
		return 1, nil
	}
	for i := range p {
		if len(r.block) == 0 {
			var idx [8]byte
			binary.LittleEndian.PutUint64(idx[:], r.blockIdx)
			r.blockIdx++
			sum := sha256.Sum256(append(append([]byte{}, r.seed[:]...), idx[:]...))
			r.block = append(r.block[:0], sum[:]...)
		}
		b := r.block[0]
		r.block = r.block[1:]
		if r.overrides != nil {
			if o, ok := r.overrides[r.pos]; ok {
				b = o
			}
		}
		p[i] = b
		r.served = append(r.served, b)
		r.pos++
	}
	return len(p), nil
}
