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
