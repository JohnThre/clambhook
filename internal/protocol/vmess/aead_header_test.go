package vmess

import (
	"bytes"
	"crypto/aes"
	"encoding/binary"
	"hash/crc32"
	"hash/fnv"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/pkg/cnet"
)

func protocolServer() protocol.Server {
	return protocol.Server{
		Name:     "test",
		Address:  "example.com:443",
		Protocol: "vmess",
		Settings: map[string]any{"uuid": testUUID, "security": "aes-128-gcm"},
	}
}

func TestCreateAuthIDDecryptable(t *testing.T) {
	var cmdKey [16]byte
	copy(cmdKey[:], bytes.Repeat([]byte{0x42}, 16))

	authID, err := createAuthID(cmdKey)
	if err != nil {
		t.Fatal(err)
	}

	block, err := aes.NewCipher(kdf16(cmdKey[:], kdfSaltAuthIDEncryptionKey))
	if err != nil {
		t.Fatal(err)
	}
	var plain [16]byte
	block.Decrypt(plain[:], authID[:])

	ts := int64(binary.BigEndian.Uint64(plain[0:8]))
	if delta := time.Now().Unix() - ts; delta < -5 || delta > 5 {
		t.Errorf("auth id timestamp delta = %d, want within +-5s", delta)
	}
	if got := crc32.ChecksumIEEE(plain[0:12]); got != binary.BigEndian.Uint32(plain[12:16]) {
		t.Errorf("auth id crc mismatch: got %#x want %#x", got, binary.BigEndian.Uint32(plain[12:16]))
	}
}

func TestEncodeRequestHeaderRoundTrip(t *testing.T) {
	cfg, err := parseConfigForTest(t)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := newSession()
	if err != nil {
		t.Fatal(err)
	}

	header, err := encodeRequestHeader(&cfg, sess, cmdTCP, "example.com:443")
	if err != nil {
		t.Fatal(err)
	}

	body := openHeaderForTest(t, cfg.cmdKey, header)

	// Validate structure of the plaintext request header.
	if body[0] != vmessVersion {
		t.Errorf("version = %#x, want %#x", body[0], vmessVersion)
	}
	if !bytes.Equal(body[1:17], sess.requestBodyIV[:]) {
		t.Error("request IV mismatch")
	}
	if !bytes.Equal(body[17:33], sess.requestBodyKey[:]) {
		t.Error("request key mismatch")
	}
	if body[33] != sess.responseHeader {
		t.Error("response auth byte mismatch")
	}
	if body[34] != optChunkStream {
		t.Errorf("options = %#x, want %#x", body[34], optChunkStream)
	}
	if body[35] != cfg.secByte {
		t.Errorf("security = %#x, want %#x", body[35], cfg.secByte)
	}
	if body[37] != cmdTCP {
		t.Errorf("command = %#x, want %#x", body[37], cmdTCP)
	}

	// FNV1a checksum covers everything before the trailing 4 bytes.
	sum := body[len(body)-4:]
	h := fnv.New32a()
	h.Write(body[:len(body)-4])
	if !bytes.Equal(sum, h.Sum(nil)) {
		t.Error("fnv checksum mismatch")
	}
}

func parseConfigForTest(t *testing.T) (config, error) {
	t.Helper()
	return parseConfig(protocolServer())
}

// openHeaderForTest reverses sealAEADHeader: it decrypts the length and payload
// AEAD blocks, mirroring what a VMESS server does on receipt.
func openHeaderForTest(t *testing.T, cmdKey [16]byte, header []byte) []byte {
	t.Helper()
	authID := header[:16]
	off := 16
	encLen := header[off : off+2+16]
	off += 2 + 16
	connNonce := header[off : off+8]
	off += 8

	lenKey := kdf16(cmdKey[:], kdfSaltVMessHeaderPayloadLengthAEADKey, authID, connNonce)
	lenNonce := kdf(cmdKey[:], kdfSaltVMessHeaderPayloadLengthAEADIV, authID, connNonce)[:12]
	lenPlain, err := cnet.AES128GCMDecrypt(lenKey, lenNonce, encLen[:2], authID, encLen[2:])
	if err != nil {
		t.Fatalf("decrypt header length: %v", err)
	}
	payLen := int(binary.BigEndian.Uint16(lenPlain))

	encPay := header[off : off+payLen+16]
	payKey := kdf16(cmdKey[:], kdfSaltVMessHeaderPayloadAEADKey, authID, connNonce)
	payNonce := kdf(cmdKey[:], kdfSaltVMessHeaderPayloadAEADIV, authID, connNonce)[:12]
	body, err := cnet.AES128GCMDecrypt(payKey, payNonce, encPay[:payLen], authID, encPay[payLen:])
	if err != nil {
		t.Fatalf("decrypt header payload: %v", err)
	}
	return body
}
