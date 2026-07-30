package vmess

import (
	"bytes"
	"io"
	"testing"
)

func TestChunkStreamRoundTrip(t *testing.T) {
	for _, security := range []string{securityAES128GCM, securityChaCha20Poly1305} {
		t.Run(security, func(t *testing.T) {
			key := bytes.Repeat([]byte{0x11}, 16)
			iv := bytes.Repeat([]byte{0x22}, 16)

			var buf bytes.Buffer
			w, err := newChunkWriter(&buf, security, key, iv)
			if err != nil {
				t.Fatal(err)
			}

			payload := bytes.Repeat([]byte("clambhook-vmess-"), 3000) // > maxBodyChunk
			if _, err := w.Write(payload); err != nil {
				t.Fatal(err)
			}

			r, err := newChunkReader(&buf, security, key, iv)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(&limitedReadAll{r: r, total: len(payload)})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("round trip mismatch: got %d bytes", len(got))
			}
		})
	}
}

func TestChunkReaderRejectsShort(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 16)
	iv := bytes.Repeat([]byte{0x22}, 16)
	// length prefix 0x0001 (< tag size) should be rejected.
	src := bytes.NewReader([]byte{0x00, 0x01, 0x00})
	r, err := newChunkReader(src, securityAES128GCM, key, iv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(make([]byte, 4)); err == nil {
		t.Fatal("expected error for short chunk length")
	}
}

func TestChunkWrongKeyFails(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 16)
	iv := bytes.Repeat([]byte{0x22}, 16)
	var buf bytes.Buffer
	w, _ := newChunkWriter(&buf, securityAES128GCM, key, iv)
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	badKey := bytes.Repeat([]byte{0x99}, 16)
	r, _ := newChunkReader(&buf, securityAES128GCM, badKey, iv)
	if _, err := r.Read(make([]byte, 8)); err == nil {
		t.Fatal("expected auth failure with wrong key")
	}
}

// TestChunkWriterFailsClosedBeforeNonceWrap verifies the H-1 remediation:
// the 16-bit chunk counter must never wrap and reuse a (key, nonce) pair.
//
// given a chunk writer whose counter is one below wrap
// when it writes the chunk that would roll the counter back to 0
// then that chunk is sealed with the final distinct nonce, and any further
// write fails closed with errBodyNonceExhausted instead of reusing a nonce.
func TestChunkWriterFailsClosedBeforeNonceWrap(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 16)
	iv := bytes.Repeat([]byte{0x22}, 16)

	var buf bytes.Buffer
	w, err := newChunkWriter(&buf, securityAES128GCM, key, iv)
	if err != nil {
		t.Fatal(err)
	}
	// Jump the counter to its last valid value so we exercise the wrap edge
	// without sealing 65k chunks.
	w.count = 0xffff
	lastNonce := append([]byte(nil), w.nonce()...)

	if _, err := w.Write([]byte("final-chunk")); err != nil {
		t.Fatalf("writing the last available chunk must succeed: %v", err)
	}
	if w.count != 0 {
		t.Fatalf("counter should have wrapped to 0, got %d", w.count)
	}

	if _, err := w.Write([]byte("one-too-many")); err != errBodyNonceExhausted {
		t.Fatalf("write past nonce space must fail closed, got %v", err)
	}
	// The exhausted writer must not have emitted a second frame that would
	// reuse nonce 0 (== the connection's very first nonce).
	firstNonce := (&chunkWriter{iv: w.iv}).nonce()
	if bytes.Equal(lastNonce, firstNonce) {
		t.Fatal("test setup invalid: last and first nonce collided")
	}
}

// given a chunk reader whose counter is one below wrap
// when it decrypts the last valid chunk and is then asked for another
// then the second read fails closed with errBodyNonceExhausted, so a hostile
// peer cannot force nonce reuse on the receive side.
func TestChunkReaderFailsClosedBeforeNonceWrap(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 16)
	iv := bytes.Repeat([]byte{0x22}, 16)

	// Produce a single valid frame sealed at count 0xffff.
	var buf bytes.Buffer
	w, err := newChunkWriter(&buf, securityAES128GCM, key, iv)
	if err != nil {
		t.Fatal(err)
	}
	w.count = 0xffff
	if _, err := w.Write([]byte("last")); err != nil {
		t.Fatal(err)
	}

	r, err := newChunkReader(&buf, securityAES128GCM, key, iv)
	if err != nil {
		t.Fatal(err)
	}
	r.count = 0xffff

	got := make([]byte, 4)
	n, err := r.Read(got)
	if err != nil {
		t.Fatalf("decrypting the last valid chunk must succeed: %v", err)
	}
	if string(got[:n]) != "last" {
		t.Fatalf("unexpected plaintext %q", got[:n])
	}
	if err := r.readChunk(); err != errBodyNonceExhausted {
		t.Fatalf("read past nonce space must fail closed, got %v", err)
	}
}

// limitedReadAll drains a reader until total bytes have been read, then reports
// EOF — the chunk reader has no framing EOF of its own in this test harness.
type limitedReadAll struct {
	r    io.Reader
	read int
	total int
}

func (l *limitedReadAll) Read(p []byte) (int, error) {
	if l.read >= l.total {
		return 0, io.EOF
	}
	if len(p) > l.total-l.read {
		p = p[:l.total-l.read]
	}
	n, err := l.r.Read(p)
	l.read += n
	return n, err
}
