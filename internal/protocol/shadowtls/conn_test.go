package shadowtls

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"hash"
	"io"
	"net"
	"testing"
	"time"
)

func newHMAC(password string, serverRandom []byte, suffix string) hash.Hash {
	h := hmac.New(sha1.New, []byte(password))
	h.Write(serverRandom)
	if suffix != "" {
		h.Write([]byte(suffix))
	}
	return h
}

// pairConns wires a client stConn and a matching server-side stConn over an
// in-memory pipe. The server's add/verify HMAC roles are the mirror image of
// the client's, exactly as a real ShadowTLS peer maintains them.
func pairConns(t *testing.T) (client, server *stConn) {
	t.Helper()
	const password = "s3cr3t"
	sr := make([]byte, tlsRandomSize)
	for i := range sr {
		sr[i] = byte(i * 7)
	}

	cc, sc := net.Pipe()
	t.Cleanup(func() { cc.Close(); sc.Close() })

	client = newStConn(cc,
		newHMAC(password, sr, "C"), // add
		newHMAC(password, sr, "S"), // verify
		nil,                        // no residual handshake frames in this test
	)
	server = newStConn(sc,
		newHMAC(password, sr, "S"), // add
		newHMAC(password, sr, "C"), // verify
		nil,
	)
	return client, server
}

func TestConnRoundTrip(t *testing.T) {
	client, server := pairConns(t)

	payload := []byte("hello shadowtls data stage round trip")
	errc := make(chan error, 1)
	go func() {
		_, err := client.Write(payload)
		errc <- err
	}()

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(server, got); err != nil {
		t.Fatalf("server read: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("client write: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("mismatch: got %q want %q", got, payload)
	}
}

func TestConnMultiFrameChaining(t *testing.T) {
	client, server := pairConns(t)

	msgs := [][]byte{[]byte("first"), []byte("second"), []byte("third-and-final")}
	go func() {
		for _, m := range msgs {
			if _, err := client.Write(m); err != nil {
				return
			}
		}
	}()

	for _, want := range msgs {
		buf := make([]byte, len(want))
		if _, err := io.ReadFull(server, buf); err != nil {
			t.Fatalf("read %q: %v", want, err)
		}
		if !bytes.Equal(buf, want) {
			t.Fatalf("frame mismatch: got %q want %q", buf, want)
		}
	}
}

func TestConnLargePayloadChunking(t *testing.T) {
	client, server := pairConns(t)

	payload := bytes.Repeat([]byte("A"), maxDataChunk*2+123)
	go func() { _, _ = client.Write(payload) }()

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(server, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("large payload mismatch (len got=%d want=%d)", len(got), len(payload))
	}
}

func TestConnRejectsTamperedFrame(t *testing.T) {
	const password = "s3cr3t"
	sr := make([]byte, tlsRandomSize)
	cc, sc := net.Pipe()
	defer cc.Close()
	defer sc.Close()

	server := newStConn(sc, newHMAC(password, sr, "S"), newHMAC(password, sr, "C"), nil)

	// Drain the client side so the server's alert write (sendAlert) doesn't
	// block on the synchronous pipe — real TCP is buffered.
	go func() { _, _ = io.Copy(io.Discard, cc) }()

	// Write a frame with a bogus HMAC directly onto the raw pipe.
	go func() {
		payload := []byte("tampered")
		frame := make([]byte, tlsHmacHeaderSize+len(payload))
		frame[0] = recordApplicationData
		frame[1], frame[2] = 3, 3
		frame[3] = 0
		frame[4] = byte(hmacSize + len(payload))
		copy(frame[tlsHeaderSize:], []byte{0xde, 0xad, 0xbe, 0xef})
		copy(frame[tlsHmacHeaderSize:], payload)
		_, _ = cc.Write(frame)
	}()

	_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	if _, err := server.Read(buf); err == nil {
		t.Fatal("expected verification failure on tampered frame")
	}
}

func TestConnFiltersResidualHandshakeFrames(t *testing.T) {
	const password = "s3cr3t"
	sr := make([]byte, tlsRandomSize)
	for i := range sr {
		sr[i] = byte(i)
	}

	cc, sc := net.Pipe()
	defer cc.Close()
	defer sc.Close()

	// Reader treats hmacIgnore (HMAC_ServerRandom) frames as residual and skips
	// them until a real HMAC_ServerRandomS frame arrives.
	reader := newStConn(sc,
		newHMAC(password, sr, "C"),
		newHMAC(password, sr, "S"),
		newHMAC(password, sr, ""), // hmacIgnore = HMAC_ServerRandom
	)
	// Writer that produces the residual frame (keyed by HMAC_ServerRandom).
	residualWriter := newStConn(cc, newHMAC(password, sr, ""), nil, nil)
	// Writer that produces the real data frame (keyed by HMAC_ServerRandomS).
	dataWriter := newStConn(cc, newHMAC(password, sr, "S"), nil, nil)

	go func() {
		_, _ = residualWriter.Write([]byte("RESIDUAL"))
		_, _ = dataWriter.Write([]byte("REALDATA"))
	}()

	got := make([]byte, len("REALDATA"))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "REALDATA" {
		t.Fatalf("got %q, want REALDATA (residual not filtered)", got)
	}
}
