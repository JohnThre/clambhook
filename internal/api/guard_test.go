package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/coder/websocket"
)

// newBoundServer returns a server whose bound address is fixed so the guard's
// Host / DNS-rebinding check is active, mirroring a started daemon.
func newBoundServer(t *testing.T, bus *events.Bus) *Server {
	t.Helper()
	srv := New(engine.New(testAuthConfig(), nil), bus)
	srv.addr = "127.0.0.1:9090"
	return srv
}

func guardRequest(t *testing.T, srv *Server, method, target, host, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if host != "" {
		req.Host = host
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	return rec
}

// TestGuardBlocksHostileBrowserOrigin is the core regression: on the default
// 127.0.0.1:9090 bind with an empty token, a hostile browser origin must be
// unable to read status or drive state-changing routes. Before the fix the
// handler answered these with 200 / a non-403 status.
func TestGuardBlocksHostileBrowserOrigin(t *testing.T) {
	srv := newBoundServer(t, nil)

	cases := []struct {
		name, method, target string
	}{
		{"read status", http.MethodGet, "/api/v1/status"},
		{"read events", http.MethodGet, "/api/v1/events"},
		{"connect", http.MethodPost, "/api/v1/connect"},
		{"disconnect", http.MethodPost, "/api/v1/disconnect"},
		{"regenerate developer CA", http.MethodPost, "/api/v1/developer/ca/regenerate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := guardRequest(t, srv, tc.method, tc.target, "127.0.0.1:9090", "http://evil.example.com")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d body=%q, want 403", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestGuardBlocksDNSRebinding covers the same-origin rebinding vector where the
// hostile page reaches the daemon via a name it controls: no Origin is sent, so
// only the Host check can reject it.
func TestGuardBlocksDNSRebinding(t *testing.T) {
	srv := newBoundServer(t, nil)

	cases := []string{
		"evil.example.com:9090",
		"evil.example.com",
		"attacker.test",
		"127.0.0.1.evil.example.com:9090",
	}
	for _, host := range cases {
		t.Run(host, func(t *testing.T) {
			rec := guardRequest(t, srv, http.MethodGet, "/api/v1/status", host, "")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("Host %q: status = %d, want 403", host, rec.Code)
			}
		})
	}
}

// TestGuardAllowsNativeAndLocalClients confirms the documented native clients
// (no Origin, loopback Host) and local browser clients (loopback Origin) keep
// working, including on state-changing routes.
func TestGuardAllowsNativeAndLocalClients(t *testing.T) {
	srv := newBoundServer(t, nil)

	readCases := []struct {
		name, host, origin string
	}{
		{"native no origin ipv4", "127.0.0.1:9090", ""},
		{"native no origin localhost", "localhost:9090", ""},
		{"same-origin browser", "127.0.0.1:9090", "http://127.0.0.1:9090"},
		{"local cross-port browser", "127.0.0.1:9090", "http://localhost:8080"},
	}
	for _, tc := range readCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := guardRequest(t, srv, http.MethodGet, "/api/v1/status", tc.host, tc.origin)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
			}
		})
	}

	// State-changing route from a native client is not blocked by the guard
	// (it reaches the handler; the handler's own result is not a 403).
	rec := guardRequest(t, srv, http.MethodPost, "/api/v1/connect", "127.0.0.1:9090", "")
	if rec.Code == http.StatusForbidden {
		t.Fatalf("native connect blocked by guard: %d body=%q", rec.Code, rec.Body.String())
	}
}

// TestGuardWildcardBindSkipsHostCheckButKeepsOrigin verifies that a wildcard
// bind (which requires a token, so the Host is unknowable) skips the Host check
// yet still rejects hostile browser origins.
func TestGuardWildcardBindSkipsHostCheckButKeepsOrigin(t *testing.T) {
	srv := New(engine.New(testAuthConfig(), nil), nil)
	srv.addr = "0.0.0.0:9090"

	rec := guardRequest(t, srv, http.MethodGet, "/api/v1/status", "some-lan-host:9090", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("wildcard bind Host check should be skipped: %d body=%q", rec.Code, rec.Body.String())
	}

	rec = guardRequest(t, srv, http.MethodGet, "/api/v1/status", "some-lan-host:9090", "http://evil.example.com")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("hostile origin should be blocked even on wildcard bind: %d", rec.Code)
	}
}

func TestOriginAllowedUnit(t *testing.T) {
	cases := []struct {
		name      string
		origin    string
		bindHost  string
		bindKnown bool
		want      bool
	}{
		{"loopback ipv4", "http://127.0.0.1:9090", "127.0.0.1", true, true},
		{"loopback ipv6", "http://[::1]:9090", "127.0.0.1", true, true},
		{"localhost", "http://localhost", "127.0.0.1", true, true},
		{"loopback cross port", "http://127.0.0.1:8080", "127.0.0.1", true, true},
		{"bind host match", "http://192.168.1.10:9090", "192.168.1.10", true, true},
		{"hostile", "http://evil.example.com", "127.0.0.1", true, false},
		{"opaque null", "null", "127.0.0.1", true, false},
		{"file scheme", "file://127.0.0.1", "127.0.0.1", true, false},
		{"empty scheme host only", "127.0.0.1:9090", "127.0.0.1", true, false},
		{"bind unknown non-loopback", "http://192.168.1.10:9090", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := originAllowed(tc.origin, tc.bindHost, tc.bindKnown)
			if got != tc.want {
				t.Fatalf("originAllowed(%q, %q, %v) = %v, want %v", tc.origin, tc.bindHost, tc.bindKnown, got, tc.want)
			}
		})
	}
}

// TestEventsWSRejectsHostileOrigin ensures the /api/v1/events WebSocket cannot
// be opened from a hostile browser origin — the guard rejects the upgrade
// before websocket.Accept runs. Before the fix InsecureSkipVerify accepted it.
func TestEventsWSRejectsHostileOrigin(t *testing.T) {
	bus := events.NewBus(events.Config{SubBufferSize: 8, RingCapacity: 8, MeterInterval: time.Hour})
	defer bus.Close()
	srv := New(engine.New(testAuthConfig(), nil), bus)
	ts := httptest.NewServer(srv.server.Handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/events"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {"http://evil.example.com"}},
	})
	if err == nil {
		c.CloseNow()
		t.Fatal("hostile-origin WebSocket dial succeeded, want rejection")
	}
}

// TestEventsWSAllowsNativeNoOrigin ensures native clients (no Origin) still
// connect to the event stream.
func TestEventsWSAllowsNativeNoOrigin(t *testing.T) {
	bus := events.NewBus(events.Config{SubBufferSize: 8, RingCapacity: 8, MeterInterval: time.Hour})
	defer bus.Close()
	srv := New(engine.New(testAuthConfig(), nil), bus)
	ts := httptest.NewServer(srv.server.Handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/events"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("native no-origin WebSocket dial failed: %v", err)
	}
	c.Close(websocket.StatusNormalClosure, "")
}
