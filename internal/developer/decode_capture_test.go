package developer

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/listener"
)

func newCaptureManager(t *testing.T) *Manager {
	t.Helper()
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled:        true,
		CaptureLimit:   10,
		BodyLimitBytes: 65536,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr
}

func TestCapturePopulatesGraphQLDecoded(t *testing.T) {
	mgr := newCaptureManager(t)
	reqBody := `{"query":"query Ping { ping }"}`
	req, err := http.NewRequest(http.MethodPost, "http://example.com/graphql", io.NopCloser(strings.NewReader(reqBody)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	tx := mgr.Begin(context.Background(), listener.HTTPCaptureMeta{Scheme: "http", Target: "example.com:80"}, req)
	req.Body = tx.RequestBody(req.Body)
	if _, err := io.Copy(io.Discard, req.Body); err != nil {
		t.Fatal(err)
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":{"ping":"pong"}}`)),
	}
	resp.Body = tx.ResponseBody(resp.Body)
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	tx.Finish(resp, nil)

	entry := mgr.List(0)[0]
	if entry.Decoded == nil || entry.Decoded.Kind != "graphql" {
		t.Fatalf("decoded = %+v", entry.Decoded)
	}
	if len(entry.Decoded.Frames) != 2 {
		t.Fatalf("frames = %d, want 2 (request+response)", len(entry.Decoded.Frames))
	}
}

func TestCapturePopulatesGRPCDecoded(t *testing.T) {
	mgr := newCaptureManager(t)
	// gRPC message: flag 0, len 2, payload {field1=varint}.
	payload := []byte{0x08, 0x2a} // field 1, varint 42
	msg := []byte{0x00}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	msg = append(msg, lenBuf[:]...)
	msg = append(msg, payload...)

	req, err := http.NewRequest(http.MethodPost, "http://example.com/pkg.Svc/Method", io.NopCloser(strings.NewReader(string(msg))))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	tx := mgr.Begin(context.Background(), listener.HTTPCaptureMeta{Scheme: "http", Target: "example.com:80"}, req)
	req.Body = tx.RequestBody(req.Body)
	if _, err := io.Copy(io.Discard, req.Body); err != nil {
		t.Fatal(err)
	}
	tx.Finish(&http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/grpc"}}}, nil)

	entry := mgr.List(0)[0]
	if entry.Decoded == nil || entry.Decoded.Kind != "grpc" {
		t.Fatalf("decoded = %+v", entry.Decoded)
	}
	if len(entry.Decoded.Frames) == 0 || !strings.Contains(entry.Decoded.Frames[0].Preview, "field 1") {
		t.Fatalf("grpc frames = %+v", entry.Decoded.Frames)
	}
}

func TestCaptureWebSocketDecoded(t *testing.T) {
	mgr := newCaptureManager(t)
	req, err := http.NewRequest(http.MethodGet, "http://example.com/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	tx := mgr.Begin(context.Background(), listener.HTTPCaptureMeta{Scheme: "http", Target: "example.com:80"}, req)

	// Server sends a single text frame "hi" after the upgrade.
	frame := []byte{0x81, 0x02, 'h', 'i'}
	resp := &http.Response{
		StatusCode: http.StatusSwitchingProtocols,
		Header: http.Header{
			"Upgrade":    []string{"websocket"},
			"Connection": []string{"Upgrade"},
		},
		Body: io.NopCloser(strings.NewReader(string(frame))),
	}
	resp.Body = tx.ResponseBody(resp.Body)
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	tx.Finish(resp, nil)

	entry := mgr.List(0)[0]
	if entry.Decoded == nil || entry.Decoded.Kind != "websocket" {
		t.Fatalf("decoded = %+v", entry.Decoded)
	}
	if len(entry.Decoded.Frames) != 1 || entry.Decoded.Frames[0].Preview != "hi" {
		t.Fatalf("ws frames = %+v", entry.Decoded.Frames)
	}
}

func TestCapturePlainRequestHasNoDecoded(t *testing.T) {
	mgr := newCaptureManager(t)
	req, err := http.NewRequest(http.MethodGet, "http://example.com/index.html", nil)
	if err != nil {
		t.Fatal(err)
	}
	tx := mgr.Begin(context.Background(), listener.HTTPCaptureMeta{Scheme: "http", Target: "example.com:80"}, req)
	tx.Finish(&http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/html"}}}, nil)

	entry := mgr.List(0)[0]
	if entry.Decoded != nil {
		t.Fatalf("expected no decoded view for plain HTML, got %+v", entry.Decoded)
	}
}

func TestStoreClonesDecoded(t *testing.T) {
	store := NewStore(4)
	original := Entry{
		ID: "x",
		Decoded: &Decoded{
			Kind:   "websocket",
			Frames: []DecodedFrame{{Direction: "server", Opcode: "text", Preview: "a"}},
		},
	}
	store.Add(original)
	// Mutate the caller's copy; the stored clone must be unaffected.
	original.Decoded.Frames[0].Preview = "mutated"

	got, ok := store.Get("x")
	if !ok {
		t.Fatal("entry not found")
	}
	if got.Decoded == nil || got.Decoded.Frames[0].Preview != "a" {
		t.Fatalf("decoded not deep-cloned: %+v", got.Decoded)
	}
}
