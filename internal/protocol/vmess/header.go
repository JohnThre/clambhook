package vmess

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"io"
	mathrand "math/rand/v2"
	"time"

	"github.com/JohnThre/clambhook/internal/protocol/v2ray"
	"github.com/google/uuid"
)

// VMess command and address-type constants (wire values).
const (
	versionByte byte = 0x01
	cmdTCP      byte = 0x01
	cmdUDP      byte = 0x02
	cmdMux      byte = 0x03

	// Option bits. S is always set (chunked stream); M enables per-chunk
	// length masking via Shake128 keyed by reqIV. G (GlobalPadding, 0x08)
	// and A (AuthenticatedLength, 0x10) are out of scope for v1 — the
	// server must therefore be willing to accept clients that set only S|M.
	optChunkStream byte = 0x01
	optChunkMask   byte = 0x04
	optChosen      byte = optChunkStream | optChunkMask
)

// cmdKeyTag is the V2Fly-mandated salt used to derive the command key from
// a raw UUID. The value is not secret — it's a constant in every VMess
// implementation — but clients and servers must agree on it bit-for-bit.
var cmdKeyTag = []byte("c48619fe-8f02-49e0-b9e9-edf763e17e21")

// requestState holds everything produced by encodeRequest that the rest of
// the client needs to run the body stream and validate the response.
type requestState struct {
	// reqKey and reqIV seed the forward-body AEAD + length-mask PRNG.
	reqKey [16]byte
	reqIV  [16]byte
	// respAuth must equal the first byte of the server's response header.
	respAuth byte
}

// encodeRequest builds and AEAD-seals a VMess AEAD request using crypto/rand
// and the current wall clock. See encodeRequestWith for the deterministic
// variant used in tests.
func encodeRequest(cfg config, cmd byte, address string) ([]byte, requestState, error) {
	return encodeRequestWith(cfg, cmd, address, rand.Reader, time.Now())
}

// encodeRequestWith is the deterministic variant. rnd supplies every piece
// of randomness (reqIV, reqKey, respAuth, padding, header nonce, and the
// 4-byte random component of the AuthID via generateAuthID's own use of
// crypto/rand — see note there). `now` drives AuthID's timestamp field.
func encodeRequestWith(cfg config, cmd byte, address string, rnd io.Reader, now time.Time) ([]byte, requestState, error) {
	var st requestState

	if _, err := io.ReadFull(rnd, st.reqIV[:]); err != nil {
		return nil, st, fmt.Errorf("vmess: random reqIV: %w", err)
	}
	if _, err := io.ReadFull(rnd, st.reqKey[:]); err != nil {
		return nil, st, fmt.Errorf("vmess: random reqKey: %w", err)
	}
	var respByte [1]byte
	if _, err := io.ReadFull(rnd, respByte[:]); err != nil {
		return nil, st, fmt.Errorf("vmess: random respAuth: %w", err)
	}
	st.respAuth = respByte[0]

	// Padding length (0..15). Random but non-crypto is fine.
	padLen := byte(mathrand.IntN(16))

	var portBytes, atypAddr []byte
	if cmd != cmdMux {
		// Address triple in V2Ray byte order. VMess, like VLESS, puts PORT
		// before ATYP||ADDR. EncodeAddr gives us ATYP||ADDR||PORT — so split.
		triple, err := v2ray.EncodeAddr(address)
		if err != nil {
			return nil, st, err
		}
		portBytes = triple[len(triple)-2:]
		atypAddr = triple[:len(triple)-2]
	}

	// Plaintext header (before FNV checksum + padding random fill).
	var hdr bytes.Buffer
	hdr.WriteByte(versionByte)
	hdr.Write(st.reqIV[:])
	hdr.Write(st.reqKey[:])
	hdr.WriteByte(st.respAuth)
	hdr.WriteByte(optChosen)
	hdr.WriteByte((padLen << 4) | cfg.security)
	hdr.WriteByte(0x00) // reserved
	hdr.WriteByte(cmd)
	hdr.Write(portBytes)
	hdr.Write(atypAddr)

	if padLen > 0 {
		pad := make([]byte, padLen)
		if _, err := io.ReadFull(rnd, pad); err != nil {
			return nil, st, fmt.Errorf("vmess: random padding: %w", err)
		}
		hdr.Write(pad)
	}

	// FNV-1a-32 checksum over the header bytes so far (spec calls this "F").
	sum := fnv.New32a()
	sum.Write(hdr.Bytes())
	var fnvBytes [4]byte
	binary.BigEndian.PutUint32(fnvBytes[:], sum.Sum32())
	hdr.Write(fnvBytes[:])

	plaintext := hdr.Bytes()

	// AEAD-seal the header per V2Fly spec.
	cmdKey := deriveCmdKey(cfg.uuid)

	authID, err := generateAuthID(cmdKey, now)
	if err != nil {
		return nil, st, err
	}

	var nonce [8]byte
	if _, err := io.ReadFull(rnd, nonce[:]); err != nil {
		return nil, st, fmt.Errorf("vmess: random header nonce: %w", err)
	}

	// Encrypt the 2-byte length field.
	lenAEAD, lenIV, err := headerAEADAndNonce(cmdKey[:], kdfLabelHeaderLenKey, kdfLabelHeaderLenIV, authID[:], nonce[:])
	if err != nil {
		return nil, st, err
	}
	var lenBE [2]byte
	binary.BigEndian.PutUint16(lenBE[:], uint16(len(plaintext)))
	encLen := lenAEAD.Seal(nil, lenIV, lenBE[:], authID[:])

	// Encrypt the header payload.
	hdrAEAD, hdrIV, err := headerAEADAndNonce(cmdKey[:], kdfLabelHeaderPayloadKey, kdfLabelHeaderPayloadIV, authID[:], nonce[:])
	if err != nil {
		return nil, st, err
	}
	encHdr := hdrAEAD.Seal(nil, hdrIV, plaintext, authID[:])

	// Wire layout: authID | encLen | nonce | encHeader.
	out := make([]byte, 0, len(authID)+len(encLen)+len(nonce)+len(encHdr))
	out = append(out, authID[:]...)
	out = append(out, encLen...)
	out = append(out, nonce[:]...)
	out = append(out, encHdr...)
	return out, st, nil
}

// deriveCmdKey computes MD5(uuid || cmdKeyTag). The 16-byte output is the
// root key for all header-layer KDF operations.
func deriveCmdKey(id uuid.UUID) [16]byte {
	h := md5.New()
	h.Write(id[:])
	h.Write(cmdKeyTag)
	var out [16]byte
	copy(out[:], h.Sum(nil))
	return out
}

// headerAEADAndNonce derives a (key, IV) pair from cmdKey + (keyLabel,
// ivLabel) + authID + headerNonce via the V2Fly KDF, then constructs an
// AES-128-GCM AEAD around the key. The derived 12-byte IV is returned
// alongside so the caller can pass it to Seal/Open directly.
func headerAEADAndNonce(cmdKey, keyLabel, ivLabel, authID, nonce []byte) (cipher.AEAD, []byte, error) {
	key := KDF(cmdKey, keyLabel, authID, nonce)[:16]
	iv := KDF(cmdKey, ivLabel, authID, nonce)[:12]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("vmess: aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("vmess: gcm: %w", err)
	}
	return aead, iv, nil
}

// readResponse decrypts the 18-byte encrypted length block, then the N-byte
// encrypted header block, and validates the echoed respAuth byte. Returns
// the 4-byte plaintext response header (V, opt, cmd, M) so callers can
// inspect opt/cmd if ever needed.
func readResponse(r io.Reader, st requestState) ([]byte, error) {
	respBodyKey := sha256.Sum256(st.reqKey[:])
	respBodyIV := sha256.Sum256(st.reqIV[:])

	lenAEAD, lenIV, err := respAEAD(respBodyKey[:16], respBodyIV[:16], []byte("AEAD Resp Header Len Key"), []byte("AEAD Resp Header Len IV"))
	if err != nil {
		return nil, err
	}
	encLen := make([]byte, 2+lenAEAD.Overhead())
	if _, err := io.ReadFull(r, encLen); err != nil {
		return nil, fmt.Errorf("vmess: read resp len: %w", err)
	}
	lenPT, err := lenAEAD.Open(nil, lenIV, encLen, nil)
	if err != nil {
		return nil, fmt.Errorf("vmess: decrypt resp len: %w", err)
	}
	respLen := int(binary.BigEndian.Uint16(lenPT))
	if respLen < 4 {
		return nil, fmt.Errorf("vmess: resp header len %d too small", respLen)
	}

	hdrAEAD, hdrIV, err := respAEAD(respBodyKey[:16], respBodyIV[:16], []byte("AEAD Resp Header Key"), []byte("AEAD Resp Header IV"))
	if err != nil {
		return nil, err
	}
	enc := make([]byte, respLen+hdrAEAD.Overhead())
	if _, err := io.ReadFull(r, enc); err != nil {
		return nil, fmt.Errorf("vmess: read resp header: %w", err)
	}
	plain, err := hdrAEAD.Open(nil, hdrIV, enc, nil)
	if err != nil {
		return nil, fmt.Errorf("vmess: decrypt resp header: %w", err)
	}
	if plain[0] != st.respAuth {
		return nil, fmt.Errorf("vmess: response V mismatch (got %#x, want %#x)", plain[0], st.respAuth)
	}
	return plain, nil
}

// respAEAD derives a per-label AEAD for the response side. VMess AEAD
// responses root their keys in SHA-256(reqKey) / SHA-256(reqIV) rather than
// cmdKey, which means knowing a user's UUID isn't enough to forge a response
// — you also need to observe (or guess) the per-request body key.
func respAEAD(respKey, respIV, keyLabel, ivLabel []byte) (cipher.AEAD, []byte, error) {
	key := KDF(respKey, keyLabel)[:16]
	iv := KDF(respIV, ivLabel)[:12]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("vmess: aes resp cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("vmess: resp gcm: %w", err)
	}
	return aead, iv, nil
}
