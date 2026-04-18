package shadowsocks

import (
	"encoding/binary"
	"fmt"
	"io"
)

// maxChunkSize is the per-chunk plaintext payload cap from the SS-AEAD-2018
// spec. The 2-byte length field reserves its top 2 bits as zero, leaving
// 14 bits of payload length (0x3FFF = 16383). Writes larger than this are
// split across multiple chunks; reads tolerate any compliant chunk size.
const maxChunkSize = 0x3FFF

// nonce is the 12-byte little-endian counter used as the AEAD nonce for one
// direction of one Shadowsocks connection. It starts at zero and increments
// once per AEAD call. **Two AEAD calls per chunk** (length frame + payload
// frame) means the nonce advances twice per Write/Read of a chunk.
type nonce [12]byte

// increment treats the bytes as a little-endian 96-bit integer and adds 1.
// We never reset; a connection that sends 2^96 chunks would wrap, but reaching
// that requires ~80 zettabytes of traffic on a single connection.
func (n *nonce) increment() {
	for i := 0; i < len(n); i++ {
		n[i]++
		if n[i] != 0 {
			return
		}
	}
}

// streamWriter wraps an io.Writer with SS-AEAD chunked framing. Per chunk:
//
//	[encrypted_length(2B BE) || length_tag(16B)]  ← AEAD call #1
//	[encrypted_payload         || payload_tag(16B)] ← AEAD call #2
//
// The nonce increments after each AEAD call. The caller is responsible for
// having sent the salt over w *before* the first Write; this type only
// handles the chunk loop.
type streamWriter struct {
	w      io.Writer
	spec   *cipherSpec
	subkey []byte
	nonce  nonce
}

func newStreamWriter(w io.Writer, spec *cipherSpec, subkey []byte) *streamWriter {
	return &streamWriter{w: w, spec: spec, subkey: subkey}
}

func (sw *streamWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > maxChunkSize {
			chunk = chunk[:maxChunkSize]
		}

		// Length frame: 2 BE bytes, encrypted under nonce N.
		lenBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBuf, uint16(len(chunk)))
		lenCT, lenTag, err := sw.spec.encrypt(sw.subkey, sw.nonce[:], lenBuf, nil)
		if err != nil {
			return written, fmt.Errorf("shadowsocks: encrypt length: %w", err)
		}
		sw.nonce.increment()

		// Payload frame: encrypted under nonce N+1.
		payloadCT, payloadTag, err := sw.spec.encrypt(sw.subkey, sw.nonce[:], chunk, nil)
		if err != nil {
			return written, fmt.Errorf("shadowsocks: encrypt payload: %w", err)
		}
		sw.nonce.increment()

		// Single Write to preserve atomicity (helps when w is a TCP socket
		// where partial writes split frames across packets).
		frame := make([]byte, 0, len(lenCT)+len(lenTag)+len(payloadCT)+len(payloadTag))
		frame = append(frame, lenCT...)
		frame = append(frame, lenTag...)
		frame = append(frame, payloadCT...)
		frame = append(frame, payloadTag...)
		if _, err := sw.w.Write(frame); err != nil {
			return written, err
		}

		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

// streamReader is the inverse of streamWriter. Reads return decrypted
// plaintext. If the caller's buffer is smaller than the next chunk, leftover
// plaintext is held in `pending` and returned on subsequent Reads.
type streamReader struct {
	r       io.Reader
	spec    *cipherSpec
	subkey  []byte
	nonce   nonce
	pending []byte
}

func newStreamReader(r io.Reader, spec *cipherSpec, subkey []byte) *streamReader {
	return &streamReader{r: r, spec: spec, subkey: subkey}
}

func (sr *streamReader) Read(p []byte) (int, error) {
	if len(sr.pending) == 0 {
		chunk, err := sr.readChunk()
		if err != nil {
			return 0, err
		}
		sr.pending = chunk
	}
	n := copy(p, sr.pending)
	sr.pending = sr.pending[n:]
	return n, nil
}

// readChunk pulls one complete chunk off the wire and returns its plaintext.
func (sr *streamReader) readChunk() ([]byte, error) {
	tagSize := sr.spec.tagSize

	// Length frame: 2 + tag bytes.
	lenFrame := make([]byte, 2+tagSize)
	if _, err := io.ReadFull(sr.r, lenFrame); err != nil {
		return nil, err
	}
	lenPT, err := sr.spec.decrypt(sr.subkey, sr.nonce[:], lenFrame[:2], nil, lenFrame[2:])
	if err != nil {
		return nil, fmt.Errorf("shadowsocks: decrypt length: %w", err)
	}
	sr.nonce.increment()

	chunkLen := int(binary.BigEndian.Uint16(lenPT))
	if chunkLen == 0 || chunkLen > maxChunkSize {
		// Spec requires top 2 bits of length to be zero (≤ 0x3FFF). A zero
		// length is illegal too — there's no use case and it would let a
		// peer spin us reading empty chunks forever.
		return nil, fmt.Errorf("shadowsocks: invalid chunk length %d", chunkLen)
	}

	// Payload frame: chunkLen + tag bytes.
	payloadFrame := make([]byte, chunkLen+tagSize)
	if _, err := io.ReadFull(sr.r, payloadFrame); err != nil {
		return nil, err
	}
	payloadPT, err := sr.spec.decrypt(sr.subkey, sr.nonce[:], payloadFrame[:chunkLen], nil, payloadFrame[chunkLen:])
	if err != nil {
		return nil, fmt.Errorf("shadowsocks: decrypt payload: %w", err)
	}
	sr.nonce.increment()

	return payloadPT, nil
}
