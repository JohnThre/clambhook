package vmess

import (
	"crypto/aes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"io"
	"time"

	"github.com/JohnThre/clambhook/pkg/cnet"
)

const (
	vmessVersion = 0x01

	// optChunkStream selects the length-prefixed AEAD chunk body stream. We do
	// not set chunk masking, global padding, or authenticated length — the
	// plain chunk stream is accepted by v2ray/xray/sing-box servers.
	optChunkStream = 0x01

	cmdTCP = 0x01
	cmdUDP = 0x02
)

// session holds the per-connection secrets negotiated in the request header.
// The response body keys are derived deterministically from the request keys so
// both sides agree without an extra exchange.
type session struct {
	requestBodyKey  [16]byte
	requestBodyIV   [16]byte
	responseBodyKey [16]byte
	responseBodyIV  [16]byte
	responseHeader  byte
}

func newSession() (*session, error) {
	var rnd [33]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, fmt.Errorf("vmess: generate session: %w", err)
	}
	s := &session{}
	copy(s.requestBodyKey[:], rnd[0:16])
	copy(s.requestBodyIV[:], rnd[16:32])
	s.responseHeader = rnd[32]

	bodyKey := sha256.Sum256(s.requestBodyKey[:])
	copy(s.responseBodyKey[:], bodyKey[:16])
	bodyIV := sha256.Sum256(s.requestBodyIV[:])
	copy(s.responseBodyIV[:], bodyIV[:16])

	return s, nil
}

// createAuthID builds the 16-byte authentication identifier: an AES-128-ECB
// single-block encryption of time(8 BE) || random(4) || CRC32(prev 12) under a
// key derived from the command key. Servers decrypt and validate the timestamp
// window plus checksum to admit the connection.
func createAuthID(cmdKey [16]byte) ([16]byte, error) {
	var out [16]byte
	var plain [16]byte
	binary.BigEndian.PutUint64(plain[0:8], uint64(time.Now().Unix()))
	if _, err := rand.Read(plain[8:12]); err != nil {
		return out, fmt.Errorf("vmess: auth id random: %w", err)
	}
	checksum := crc32.ChecksumIEEE(plain[0:12])
	binary.BigEndian.PutUint32(plain[12:16], checksum)

	block, err := aes.NewCipher(kdf16(cmdKey[:], kdfSaltAuthIDEncryptionKey))
	if err != nil {
		return out, fmt.Errorf("vmess: auth id cipher: %w", err)
	}
	block.Encrypt(out[:], plain[:])
	return out, nil
}

// encodeRequestHeader assembles the plaintext request header and wraps it in the
// VMESS-AEAD envelope: authID || sealed(length) || nonce || sealed(payload).
func encodeRequestHeader(cfg *config, sess *session, cmd byte, target string) ([]byte, error) {
	addr, err := encodeAddr(target)
	if err != nil {
		return nil, err
	}

	body := make([]byte, 0, 1+16+16+1+1+1+1+1+len(addr)+4)
	body = append(body, vmessVersion)
	body = append(body, sess.requestBodyIV[:]...)
	body = append(body, sess.requestBodyKey[:]...)
	body = append(body, sess.responseHeader)
	body = append(body, optChunkStream)
	// padding length (high 4 bits) is 0; low 4 bits carry the security selector.
	body = append(body, cfg.secByte)
	body = append(body, 0x00) // reserved
	body = append(body, cmd)
	body = append(body, addr...)

	h := fnv.New32a()
	h.Write(body)
	body = h.Sum(body)

	return sealAEADHeader(cfg.cmdKey, body)
}

// sealAEADHeader implements the VMESS-AEAD header envelope.
func sealAEADHeader(cmdKey [16]byte, data []byte) ([]byte, error) {
	authID, err := createAuthID(cmdKey)
	if err != nil {
		return nil, err
	}

	var connNonce [8]byte
	if _, err := rand.Read(connNonce[:]); err != nil {
		return nil, fmt.Errorf("vmess: header nonce: %w", err)
	}

	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(data)))

	lenKey := kdf16(cmdKey[:], kdfSaltVMessHeaderPayloadLengthAEADKey, authID[:], connNonce[:])
	lenNonce := kdf(cmdKey[:], kdfSaltVMessHeaderPayloadLengthAEADIV, authID[:], connNonce[:])[:12]
	lenCT, lenTag, err := cnet.AES128GCMEncrypt(lenKey, lenNonce, lenBuf[:], authID[:])
	if err != nil {
		return nil, fmt.Errorf("vmess: seal header length: %w", err)
	}

	payKey := kdf16(cmdKey[:], kdfSaltVMessHeaderPayloadAEADKey, authID[:], connNonce[:])
	payNonce := kdf(cmdKey[:], kdfSaltVMessHeaderPayloadAEADIV, authID[:], connNonce[:])[:12]
	payCT, payTag, err := cnet.AES128GCMEncrypt(payKey, payNonce, data, authID[:])
	if err != nil {
		return nil, fmt.Errorf("vmess: seal header payload: %w", err)
	}

	out := make([]byte, 0, 16+len(lenCT)+len(lenTag)+8+len(payCT)+len(payTag))
	out = append(out, authID[:]...)
	out = append(out, lenCT...)
	out = append(out, lenTag...)
	out = append(out, connNonce[:]...)
	out = append(out, payCT...)
	out = append(out, payTag...)
	return out, nil
}

// readResponseHeader reads and verifies the AEAD-sealed response header. The
// first plaintext byte must equal the response-auth byte we sent, proving the
// server holds the shared key.
func readResponseHeader(r io.Reader, sess *session) error {
	lenKey := kdf16(sess.responseBodyKey[:], kdfSaltAEADRespHeaderLenKey)
	lenNonce := kdf(sess.responseBodyIV[:], kdfSaltAEADRespHeaderLenIV)[:12]

	encLen := make([]byte, 2+16)
	if _, err := io.ReadFull(r, encLen); err != nil {
		return fmt.Errorf("vmess: read response length: %w", err)
	}
	lenPlain, err := cnet.AES128GCMDecrypt(lenKey, lenNonce, encLen[:2], nil, encLen[2:])
	if err != nil {
		return fmt.Errorf("vmess: decrypt response length: %w", err)
	}
	headerLen := int(binary.BigEndian.Uint16(lenPlain))

	payKey := kdf16(sess.responseBodyKey[:], kdfSaltAEADRespHeaderPayloadKey)
	payNonce := kdf(sess.responseBodyIV[:], kdfSaltAEADRespHeaderPayloadIV)[:12]

	encHeader := make([]byte, headerLen+16)
	if _, err := io.ReadFull(r, encHeader); err != nil {
		return fmt.Errorf("vmess: read response header: %w", err)
	}
	header, err := cnet.AES128GCMDecrypt(payKey, payNonce, encHeader[:headerLen], nil, encHeader[headerLen:])
	if err != nil {
		return fmt.Errorf("vmess: decrypt response header: %w", err)
	}
	if len(header) < 1 || header[0] != sess.responseHeader {
		return fmt.Errorf("vmess: response authentication mismatch")
	}
	// header[1] option, header[2] command, header[3] command length, then a
	// dynamic-port command payload we don't act on. It's already been read and
	// verified as part of the AEAD payload.
	return nil
}
