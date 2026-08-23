package listener

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// timingInspector embeds the no-op disabledMITMInspector and returns an
// inspection that records the SetTimings call, proving the listener exercises
// the optional HTTPInspectionTimings interface in the forward path.
type timingInspector struct {
	disabledMITMInspector
	mu     sync.Mutex
	got    HTTPTimings
	called bool
}

func (i *timingInspector) Begin(context.Context, HTTPCaptureMeta, *http.Request) HTTPInspection {
	return &timingInspection{insp: i}
}

type timingInspection struct {
	insp *timingInspector
}

func (t *timingInspection) RequestBody(b io.ReadCloser) io.ReadCloser  { return b }
func (t *timingInspection) ResponseBody(b io.ReadCloser) io.ReadCloser { return b }
func (t *timingInspection) Finish(*http.Response, error)               {}
func (t *timingInspection) SetTimings(got HTTPTimings) {
	t.insp.mu.Lock()
	t.insp.got = got
	t.insp.called = true
	t.insp.mu.Unlock()
}

func TestHTTPForwardRecordsTimings(t *testing.T) {
	remoteCh := make(chan net.Conn, 1)
	inspector := &timingInspector{}
	_, addr := newTestHTTPListenerWithOpts(t, stubDial(remoteCh), Options{HTTPInspector: inspector})

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()

	errCh := make(chan error, 1)
	go func() {
		remote := <-remoteCh
		defer remote.Close()
		if _, err := http.ReadRequest(bufio.NewReader(remote)); err != nil {
			errCh <- err
			return
		}
		// Simulate server processing so the wait (TTFB) phase is non-zero.
		time.Sleep(30 * time.Millisecond)
		if _, err := io.WriteString(remote, "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	payload := "GET http://example.com/path HTTP/1.1\r\nHost: example.com\r\n\r\n"
	if _, err := io.WriteString(client, payload); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(client)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	inspector.mu.Lock()
	called, got := inspector.called, inspector.got
	inspector.mu.Unlock()

	if !called {
		t.Fatal("SetTimings was not called on the forward path")
	}
	// Plain HTTP has no TLS, so SSL must be zero.
	if got.SSL != 0 {
		t.Fatalf("plain HTTP SSL = %v, want 0", got.SSL)
	}
	// The remote slept 30ms before responding, so wait (TTFB) must be positive.
	if got.Wait <= 0 {
		t.Fatalf("wait = %v, want > 0", got.Wait)
	}
	// The other phases are non-negative for a successful transfer.
	if got.Connect < 0 || got.Send < 0 || got.Receive < 0 {
		t.Fatalf("negative phase: %+v", got)
	}
}

func TestHTTPTimingsZeroTimestampsDegrade(t *testing.T) {
	// A recorder that never set any milestone yields all-zero durations, not
	// negative ones, so partial captures (an error before a later phase) are
	// safe.
	got := httpTimings{}.finish()
	if got.Connect != 0 || got.SSL != 0 || got.Send != 0 || got.Wait != 0 || got.Receive != 0 {
		t.Fatalf("zero recorder = %+v, want all zero", got)
	}
}

func TestSetHTTPInspectionTimingsNilSafe(t *testing.T) {
	// A nil inspection (disabled manager) and an inspection that does not
	// implement HTTPInspectionTimings must both be no-ops.
	setHTTPInspectionTimings(nil, httpTimings{dialStart: time.Now()})
	setHTTPInspectionTimings(&nilTimingsInspection{}, httpTimings{dialStart: time.Now()})
}

type nilTimingsInspection struct{}

func (*nilTimingsInspection) RequestBody(b io.ReadCloser) io.ReadCloser  { return b }
func (*nilTimingsInspection) ResponseBody(b io.ReadCloser) io.ReadCloser { return b }
func (*nilTimingsInspection) Finish(*http.Response, error)               {}
