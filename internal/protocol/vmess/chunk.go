package vmess

import (
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/crypto/sha3"
)

// Max plaintext chunk size (matches v2ray-core's default). The wire length
// field is 16 bits, so the hard ceiling is ~65519 bytes; sticking with 16K
// keeps per-chunk memory predictable.
const maxChunkPlaintext = 16 * 1024

// chunkCodec encapsulates the state for one direction (read OR write) of
// a VMess body stream with options S|M (ChunkStream + ChunkMask):
//
//   - Each chunk on the wire is [ masked_len(2B BE) | ciphertext+tag ].
//   - masked_len = actual_len XOR next 2 bytes of Shake128(iv).
//   - Per-chunk AEAD nonce is counter(2B BE) || iv[2:12]; counter++ per chunk.
//
// The Shake128 reader is a keyed stream — it produces an infinite sequence
// of pseudorandom bytes once seeded with iv, so every chunk reads 2 fresh
// bytes in-order. Writer and reader on opposite ends must consume it in
// lockstep or frames desynchronize.
type chunkCodec struct {
	aead     cipher.AEAD
	ivSuffix [10]byte // bytes 2..12 of reqIV (or respIV for read side)
	mask     sha3.ShakeHash
	counter  uint16
}

func newChunkCodec(aead cipher.AEAD, iv [16]byte) *chunkCodec {
	c := &chunkCodec{aead: aead}
	copy(c.ivSuffix[:], iv[2:12])
	c.mask = sha3.NewShake128()
	c.mask.Write(iv[:])
	return c
}

// seal writes one chunk to w. Returns n = len(plaintext) on success.
func (c *chunkCodec) seal(w io.Writer, plaintext []byte) (int, error) {
	if len(plaintext) == 0 {
		return 0, nil
	}
	if len(plaintext) > maxChunkPlaintext {
		return 0, fmt.Errorf("vmess: chunk plaintext %d exceeds max", len(plaintext))
	}

	var nonce [12]byte
	binary.BigEndian.PutUint16(nonce[0:2], c.counter)
	copy(nonce[2:], c.ivSuffix[:])

	ct := c.aead.Seal(nil, nonce[:], plaintext, nil)
	c.counter++

	var maskBytes [2]byte
	if _, err := io.ReadFull(c.mask, maskBytes[:]); err != nil {
		return 0, fmt.Errorf("vmess: shake mask: %w", err)
	}
	var lenBE [2]byte
	binary.BigEndian.PutUint16(lenBE[:], uint16(len(ct)))
	lenBE[0] ^= maskBytes[0]
	lenBE[1] ^= maskBytes[1]

	// Write length and ciphertext as a single Write so TCP/TLS record
	// boundaries line up with chunk boundaries (matches trojan/shadowsocks
	// pattern — prevents half-chunk stalls on small MTU links).
	frame := make([]byte, 0, 2+len(ct))
	frame = append(frame, lenBE[:]...)
	frame = append(frame, ct...)
	if _, err := w.Write(frame); err != nil {
		return 0, err
	}
	return len(plaintext), nil
}

// open reads one chunk from r and returns its plaintext.
func (c *chunkCodec) open(r io.Reader) ([]byte, error) {
	var lenBE [2]byte
	if _, err := io.ReadFull(r, lenBE[:]); err != nil {
		return nil, err
	}
	var maskBytes [2]byte
	if _, err := io.ReadFull(c.mask, maskBytes[:]); err != nil {
		return nil, fmt.Errorf("vmess: shake mask: %w", err)
	}
	lenBE[0] ^= maskBytes[0]
	lenBE[1] ^= maskBytes[1]

	chunkLen := int(binary.BigEndian.Uint16(lenBE[:]))
	if chunkLen < c.aead.Overhead() {
		return nil, fmt.Errorf("vmess: chunk length %d smaller than tag", chunkLen)
	}
	if chunkLen > maxChunkPlaintext+c.aead.Overhead() {
		return nil, fmt.Errorf("vmess: chunk length %d exceeds max", chunkLen)
	}

	ct := make([]byte, chunkLen)
	if _, err := io.ReadFull(r, ct); err != nil {
		return nil, err
	}

	var nonce [12]byte
	binary.BigEndian.PutUint16(nonce[0:2], c.counter)
	copy(nonce[2:], c.ivSuffix[:])

	pt, err := c.aead.Open(nil, nonce[:], ct, nil)
	if err != nil {
		return nil, fmt.Errorf("vmess: decrypt chunk: %w", err)
	}
	c.counter++
	return pt, nil
}
