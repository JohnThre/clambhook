// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package vmess

import (
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/JohnThre/clambhook/pkg/cnet"
)

// maxBodyChunk caps plaintext per chunk. The 2-byte length prefix carries the
// encrypted size (ciphertext + 16-byte tag), so the ceiling is 65535-16; we use
// a smaller, allocation-friendly bound for writes and tolerate any compliant
// size on reads.
const maxBodyChunk = 16384

// errBodyNonceExhausted is returned once a chunk stream has consumed every
// distinct nonce for its key. The AEAD nonce embeds the chunk counter as a
// 16-bit big-endian value (count(2 BE) || IV[2:12]), so counts 0..65535 yield
// distinct nonces; the next chunk would wrap the counter to 0 and reuse the
// (key, nonce) pair. Nonce reuse in AES-GCM / ChaCha20-Poly1305 is catastrophic
// (it leaks the XOR of plaintexts and enables tag forgery), so the stream is
// torn down before the counter can wrap. The 16-bit counter is fixed by the
// VMess wire format and cannot be widened without breaking interoperability;
// callers must reconnect (re-key) to continue, deriving a fresh key + IV. See
// docs/security-review.md (finding H-1).
var errBodyNonceExhausted = errors.New("vmess: body nonce counter exhausted; reconnect required")

// sealFunc / openFunc abstract the two supported AEAD ciphers so the chunk
// stream code is cipher-agnostic.
type sealFunc func(nonce, plaintext []byte) (ciphertext, tag []byte, err error)
type openFunc func(nonce, ciphertext, tag []byte) (plaintext []byte, err error)

// chacha20Key expands a 16-byte VMESS body key into the 32-byte key ChaCha20
// expects, using the MD5 chain v2ray/xray/sing-box use for compatibility.
func chacha20Key(key []byte) []byte {
	out := make([]byte, 32)
	t := md5.Sum(key)
	copy(out[:16], t[:])
	t = md5.Sum(out[:16])
	copy(out[16:], t[:])
	return out
}

func newSeal(security string, key []byte) (sealFunc, error) {
	switch security {
	case securityAES128GCM:
		k := append([]byte(nil), key...)
		return func(nonce, pt []byte) ([]byte, []byte, error) {
			return cnet.AES128GCMEncrypt(k, nonce, pt, nil)
		}, nil
	case securityChaCha20Poly1305:
		k := chacha20Key(key)
		return func(nonce, pt []byte) ([]byte, []byte, error) {
			return cnet.ChaCha20Poly1305Encrypt(k, nonce, pt, nil)
		}, nil
	default:
		return nil, fmt.Errorf("vmess: unsupported security %q", security)
	}
}

func newOpen(security string, key []byte) (openFunc, error) {
	switch security {
	case securityAES128GCM:
		k := append([]byte(nil), key...)
		return func(nonce, ct, tag []byte) ([]byte, error) {
			return cnet.AES128GCMDecrypt(k, nonce, ct, nil, tag)
		}, nil
	case securityChaCha20Poly1305:
		k := chacha20Key(key)
		return func(nonce, ct, tag []byte) ([]byte, error) {
			return cnet.ChaCha20Poly1305Decrypt(k, nonce, ct, nil, tag)
		}, nil
	default:
		return nil, fmt.Errorf("vmess: unsupported security %q", security)
	}
}

// chunkWriter frames outbound data as length-prefixed AEAD chunks. The 12-byte
// nonce is count(2 BE) || IV[2:12], with count incrementing once per chunk.
type chunkWriter struct {
	w         io.Writer
	seal      sealFunc
	iv        [16]byte
	count     uint16
	exhausted bool
}

func newChunkWriter(w io.Writer, security string, key, iv []byte) (*chunkWriter, error) {
	seal, err := newSeal(security, key)
	if err != nil {
		return nil, err
	}
	cw := &chunkWriter{w: w, seal: seal}
	copy(cw.iv[:], iv)
	return cw, nil
}

func (cw *chunkWriter) nonce() []byte {
	var n [12]byte
	binary.BigEndian.PutUint16(n[0:2], cw.count)
	copy(n[2:], cw.iv[2:12])
	return n[:]
}

func (cw *chunkWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if cw.exhausted {
			return written, errBodyNonceExhausted
		}
		chunk := p
		if len(chunk) > maxBodyChunk {
			chunk = chunk[:maxBodyChunk]
		}
		ct, tag, err := cw.seal(cw.nonce(), chunk)
		if err != nil {
			return written, fmt.Errorf("vmess: seal chunk: %w", err)
		}
		cw.count++
		if cw.count == 0 {
			// The counter just wrapped back to 0: every distinct nonce for
			// this key has now been used. Refuse further writes so the next
			// chunk can never reuse a (key, nonce) pair.
			cw.exhausted = true
		}

		frame := make([]byte, 2+len(ct)+len(tag))
		binary.BigEndian.PutUint16(frame[0:2], uint16(len(ct)+len(tag)))
		copy(frame[2:], ct)
		copy(frame[2+len(ct):], tag)
		if _, err := cw.w.Write(frame); err != nil {
			return written, fmt.Errorf("vmess: write chunk: %w", err)
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

// chunkReader reverses chunkWriter. Decrypted plaintext is buffered so callers
// can Read with arbitrarily small buffers.
type chunkReader struct {
	r         io.Reader
	open      openFunc
	iv        [16]byte
	count     uint16
	exhausted bool
	buf       []byte
}

func newChunkReader(r io.Reader, security string, key, iv []byte) (*chunkReader, error) {
	open, err := newOpen(security, key)
	if err != nil {
		return nil, err
	}
	cr := &chunkReader{r: r, open: open}
	copy(cr.iv[:], iv)
	return cr, nil
}

func (cr *chunkReader) nonce() []byte {
	var n [12]byte
	binary.BigEndian.PutUint16(n[0:2], cr.count)
	copy(n[2:], cr.iv[2:12])
	return n[:]
}

func (cr *chunkReader) Read(p []byte) (int, error) {
	// Loop past empty chunks so we never return (0, nil), which the io.Reader
	// contract discourages and which can make tight-looping callers spin.
	for len(cr.buf) == 0 {
		if err := cr.readChunk(); err != nil {
			return 0, err
		}
	}
	n := copy(p, cr.buf)
	cr.buf = cr.buf[n:]
	return n, nil
}

func (cr *chunkReader) readChunk() error {
	if cr.exhausted {
		return errBodyNonceExhausted
	}
	var lb [2]byte
	if _, err := io.ReadFull(cr.r, lb[:]); err != nil {
		return err
	}
	length := int(binary.BigEndian.Uint16(lb[:]))
	if length < 16 {
		return fmt.Errorf("vmess: chunk length %d too small", length)
	}
	frame := make([]byte, length)
	if _, err := io.ReadFull(cr.r, frame); err != nil {
		return fmt.Errorf("vmess: read chunk: %w", err)
	}
	ct := frame[:length-16]
	tag := frame[length-16:]
	pt, err := cr.open(cr.nonce(), ct, tag)
	if err != nil {
		return fmt.Errorf("vmess: open chunk: %w", err)
	}
	cr.count++
	if cr.count == 0 {
		// Counter wrapped: a compliant peer never sends this many chunks on
		// one key. Refuse to decrypt further so a malicious peer cannot force
		// nonce reuse on our side.
		cr.exhausted = true
	}
	cr.buf = pt
	return nil
}
