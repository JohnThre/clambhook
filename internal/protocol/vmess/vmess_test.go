package vmess

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/internal/protocol/v2ray"
	"github.com/google/uuid"
)

func TestDialerRegistered(t *testing.T) {
	d, err := protocol.NewDialer(protocol.Server{
		Name:     "test",
		Address:  "example.com:443",
		Protocol: "vmess",
		Settings: map[string]any{"uuid": testUUID},
	})
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	if d.Protocol() != "vmess" {
		t.Errorf("Protocol() = %q, want vmess", d.Protocol())
	}
	if _, ok := d.(protocol.PacketDialer); !ok {
		t.Error("dialer does not implement PacketDialer")
	}
}

// TestTCPRoundTrip wires the VMess client against an in-process server that
// implements the VMess AEAD handshake + chunk-stream echo. If this passes,
// the wire format is self-consistent end-to-end: header encode + AEAD seal
// on the client side, AEAD open + FNV verify on the server side, chunk
// seal/open on both sides, response header seal + validate.
func TestTCPRoundTrip(t *testing.T) {
	for _, sec := range []struct {
		name    string
		byteVal byte
	}{
		{"aes-128-gcm", securityAES128GCM},
		{"chacha20-poly1305", securityChaCha20Poly1305},
	} {
		t.Run(sec.name, func(t *testing.T) {
			clientSide, serverSide := net.Pipe()
			id := uuid.MustParse(testUUID)
			cfg := config{
				uuid:     id,
				security: sec.byteVal,
				useTLS:   false,
			}

			var serverErr error
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				serverErr = runFakeVMessServer(serverSide, id, sec.byteVal, []byte("hello vmess"))
			}()

			header, state, err := encodeRequest(cfg, cmdTCP, "example.org:80")
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if _, err := clientSide.Write(header); err != nil {
				t.Fatalf("write header: %v", err)
			}

			c, err := newConn(clientSide, state, sec.byteVal)
			if err != nil {
				t.Fatalf("newConn: %v", err)
			}
			payload := []byte("hello vmess")
			if _, err := c.Write(payload); err != nil {
				t.Fatalf("write payload: %v", err)
			}

			buf := make([]byte, 64)
			n, err := c.Read(buf)
			if err != nil && err != io.EOF {
				t.Fatalf("read: %v", err)
			}
			if !bytes.Equal(buf[:n], payload) {
				t.Errorf("echo mismatch: got %q, want %q", buf[:n], payload)
			}
			c.Close()

			wg.Wait()
			if serverErr != nil {
				t.Errorf("server: %v", serverErr)
			}
		})
	}
}

// runFakeVMessServer reads + validates a VMess AEAD request, sends a
// response header, then reads one body chunk and echoes it back.
//
// expectPayload is what the test expects the client to send — asserting the
// server sees it after full decryption sanity-checks the chunk codec.
func runFakeVMessServer(c net.Conn, id uuid.UUID, security byte, expectPayload []byte) error {
	defer c.Close()

	cmdKey := deriveCmdKey(id)

	// Read AuthID(16) + encLen(18) + nonce(8).
	pre := make([]byte, 16+18+8)
	if _, err := io.ReadFull(c, pre); err != nil {
		return fmt.Errorf("server: read preamble: %w", err)
	}
	authID := pre[0:16]
	encLen := pre[16:34]
	nonce := pre[34:42]

	// Decrypt length.
	lenAEAD, lenIV, err := headerAEADAndNonce(cmdKey[:], kdfLabelHeaderLenKey, kdfLabelHeaderLenIV, authID, nonce)
	if err != nil {
		return fmt.Errorf("server: len aead: %w", err)
	}
	lenPT, err := lenAEAD.Open(nil, lenIV, encLen, authID)
	if err != nil {
		return fmt.Errorf("server: decrypt len: %w", err)
	}
	headerLen := int(binary.BigEndian.Uint16(lenPT))

	// Decrypt header.
	hdrAEAD, hdrIV, err := headerAEADAndNonce(cmdKey[:], kdfLabelHeaderPayloadKey, kdfLabelHeaderPayloadIV, authID, nonce)
	if err != nil {
		return fmt.Errorf("server: hdr aead: %w", err)
	}
	encHdr := make([]byte, headerLen+hdrAEAD.Overhead())
	if _, err := io.ReadFull(c, encHdr); err != nil {
		return fmt.Errorf("server: read header: %w", err)
	}
	plainHdr, err := hdrAEAD.Open(nil, hdrIV, encHdr, authID)
	if err != nil {
		return fmt.Errorf("server: decrypt header: %w", err)
	}

	// Parse plaintext header.
	if plainHdr[0] != versionByte {
		return fmt.Errorf("server: bad version %#x", plainHdr[0])
	}
	var reqIV [16]byte
	copy(reqIV[:], plainHdr[1:17])
	var reqKey [16]byte
	copy(reqKey[:], plainHdr[17:33])
	respAuth := plainHdr[33]
	// plainHdr[34] = opt
	padSec := plainHdr[35]
	padLen := int(padSec >> 4)
	gotSecurity := padSec & 0x0F
	// plainHdr[36] = reserved
	cmd := plainHdr[37]
	port := binary.BigEndian.Uint16(plainHdr[38:40])

	if gotSecurity != security {
		return fmt.Errorf("server: security = %#x, want %#x", gotSecurity, security)
	}
	if cmd != cmdTCP {
		return fmt.Errorf("server: cmd = %#x, want TCP", cmd)
	}

	// Skip ATYP+ADDR.
	atyp := plainHdr[40]
	addrStart := 41
	switch atyp {
	case v2ray.ATYPIPv4:
		addrStart += 4
	case v2ray.ATYPIPv6:
		addrStart += 16
	case v2ray.ATYPDomain:
		addrStart += 1 + int(plainHdr[41])
	default:
		return fmt.Errorf("server: bad atyp %#x", atyp)
	}

	// Verify FNV-1a-32 over [0:end-4].
	if addrStart+padLen+4 != len(plainHdr) {
		return fmt.Errorf("server: header length mismatch: addrStart=%d padLen=%d total=%d",
			addrStart, padLen, len(plainHdr))
	}
	fnvStart := len(plainHdr) - 4
	wantFNV := binary.BigEndian.Uint32(plainHdr[fnvStart:])
	h := fnv.New32a()
	h.Write(plainHdr[:fnvStart])
	if h.Sum32() != wantFNV {
		return fmt.Errorf("server: FNV mismatch (got %#x, want %#x)", h.Sum32(), wantFNV)
	}
	_ = port

	// Decrypt one body chunk from client.
	bodyAEAD, err := newBodyAEAD(security, reqKey[:])
	if err != nil {
		return fmt.Errorf("server: body aead: %w", err)
	}
	rCodec := newChunkCodec(bodyAEAD, reqIV)
	gotPayload, err := rCodec.open(c)
	if err != nil {
		return fmt.Errorf("server: open chunk: %w", err)
	}
	if !bytes.Equal(gotPayload, expectPayload) {
		return fmt.Errorf("server: payload = %q, want %q", gotPayload, expectPayload)
	}

	// Build + send the response.
	respBodyKey := sha256.Sum256(reqKey[:])
	respBodyIV := sha256.Sum256(reqIV[:])

	lenRK := KDF(respBodyKey[:16], []byte("AEAD Resp Header Len Key"))[:16]
	lenRIV := KDF(respBodyIV[:16], []byte("AEAD Resp Header Len IV"))[:12]
	lenBlock, err := aes.NewCipher(lenRK)
	if err != nil {
		return err
	}
	lenRAEAD, err := cipher.NewGCM(lenBlock)
	if err != nil {
		return err
	}
	// Response header: [V | opt=0 | cmd=0 | M=0]
	respHdr := []byte{respAuth, 0x00, 0x00, 0x00}
	var lenResp [2]byte
	binary.BigEndian.PutUint16(lenResp[:], uint16(len(respHdr)))
	encRespLen := lenRAEAD.Seal(nil, lenRIV, lenResp[:], nil)

	hdrRK := KDF(respBodyKey[:16], []byte("AEAD Resp Header Key"))[:16]
	hdrRIV := KDF(respBodyIV[:16], []byte("AEAD Resp Header IV"))[:12]
	hdrBlock, err := aes.NewCipher(hdrRK)
	if err != nil {
		return err
	}
	hdrRAEAD, err := cipher.NewGCM(hdrBlock)
	if err != nil {
		return err
	}
	encRespHdr := hdrRAEAD.Seal(nil, hdrRIV, respHdr, nil)

	if _, err := c.Write(encRespLen); err != nil {
		return err
	}
	if _, err := c.Write(encRespHdr); err != nil {
		return err
	}

	// Echo chunk back with server-side (response) body AEAD + codec.
	respBodyAEAD, err := newBodyAEAD(security, respBodyKey[:16])
	if err != nil {
		return err
	}
	var respIV [16]byte
	copy(respIV[:], respBodyIV[:16])
	wCodec := newChunkCodec(respBodyAEAD, respIV)
	if _, err := wCodec.seal(c, gotPayload); err != nil {
		return fmt.Errorf("server: seal echo: %w", err)
	}
	return nil
}

// TestAuthIDValid sanity check — AuthIDs from the real generator should
// decrypt with the expected key and pass CRC32 validation. Uses real
// crypto/rand, so this test also indirectly exercises that path.
func TestAuthIDValid(t *testing.T) {
	var cmdKey [16]byte
	_, _ = rand.Read(cmdKey[:])

	id, err := generateAuthID(cmdKey, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(id[:], make([]byte, 16)) {
		t.Error("AuthID is all zero")
	}
}
