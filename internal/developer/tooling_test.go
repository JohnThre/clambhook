// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package developer

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
)

const publicTestIP = "93.184.216.34"

func publicRepeatURL(srv *httptest.Server, path string) string {
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return "http://" + publicTestIP + ":" + port + path
}

func publicRepeatClient(srv *httptest.Server) *http.Client {
	dialAddr := srv.Listener.Addr().String()
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, dialAddr)
			},
		},
	}
}

func withRepeatClient(srv *httptest.Server) func() {
	old := repeatHTTPClient
	repeatHTTPClient = publicRepeatClient(srv)
	return func() { repeatHTTPClient = old }
}

func TestMapLocalServesConfiguredFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.txt")
	if err := os.WriteFile(path, []byte("local body"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled: true,
		MapRules: []config.DeveloperMapRuleConfig{{
			ID:        "local",
			Enabled:   true,
			Kind:      "local",
			LocalPath: path,
			Match: config.DeveloperMatchConfig{
				Methods: []string{http.MethodGet},
				Host:    "example.com",
			},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	_, result, err := mgr.MapRequest(req)
	if err != nil {
		t.Fatalf("MapRequest: %v", err)
	}
	if result == nil || result.Local == nil {
		t.Fatalf("result = %+v, want local map", result)
	}
	if got := string(result.Local.Body); got != "local body" {
		t.Fatalf("body = %q", got)
	}
}

func TestMapRemoteRewritesURLWithPathPrefix(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled: true,
		MapRules: []config.DeveloperMapRuleConfig{{
			ID:        "remote",
			Enabled:   true,
			Kind:      "remote",
			RemoteURL: "https://mirror.example/v2",
			Match: config.DeveloperMatchConfig{
				PathPrefix: "/api",
			},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://origin.example/api/users?q=1", nil)
	rewritten, result, err := mgr.MapRequest(req)
	if err != nil {
		t.Fatalf("MapRequest: %v", err)
	}
	if result == nil || result.Kind != "remote" {
		t.Fatalf("result = %+v, want remote map", result)
	}
	if got, want := rewritten.URL.String(), "https://mirror.example/v2/users?q=1"; got != want {
		t.Fatalf("rewritten URL = %q, want %q", got, want)
	}
}

func TestRepeatOmitsRedactedHeaders(t *testing.T) {
	gotAuth := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	defer withRepeatClient(server)()

	mgr, err := NewManager(config.DeveloperConfig{
		Enabled:       true,
		CaptureLimit:  10,
		RedactHeaders: []string{"authorization"},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.store.Add(Entry{
		ID:     "dev-1",
		Method: http.MethodPost,
		URL:    publicRepeatURL(server, "/x"),
		Request: Message{
			Headers: []Header{{Name: "Authorization", Value: redactedValue, Redacted: true}},
			Body:    Body{Preview: "hello", Size: 5, PreviewBytes: 5},
		},
	})
	resp, err := mgr.Repeat(context.Background(), RepeatRequest{EntryID: "dev-1"})
	if err != nil {
		t.Fatalf("Repeat: %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization replayed as %q", gotAuth)
	}
	if resp.Entry.Status != http.StatusOK || resp.Entry.Response.Body.Preview != "ok" {
		t.Fatalf("repeat response = %+v", resp.Entry)
	}
}

func TestSendStandalone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Compose") != "yes" {
			t.Errorf("unexpected request: %s %+v", r.Method, r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"a":1}` {
			t.Errorf("body = %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	defer withRepeatClient(server)()

	mgr, err := NewManager(config.DeveloperConfig{Enabled: true, CaptureLimit: 10})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	body := `{"a":1}`
	resp, err := mgr.Send(context.Background(), ComposedRequest{
		Method:  http.MethodPost,
		URL:     publicRepeatURL(server, "/api"),
		Headers: []Header{{Name: "X-Compose", Value: "yes"}, {Name: "Content-Type", Value: "application/json"}},
		Body:    &body,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp.Entry.Status != http.StatusOK {
		t.Fatalf("status = %d", resp.Entry.Status)
	}
	if resp.Entry.Method != "POST" || resp.Entry.Request.Body.Preview != `{"a":1}` {
		t.Fatalf("captured request = %+v", resp.Entry.Request)
	}
	// The composed request body gets a viewer (pretty-print) daemon-side.
	if resp.Entry.Request.Body.Viewer == nil || resp.Entry.Request.Body.Viewer.Kind != "json" {
		t.Fatalf("request viewer = %+v", resp.Entry.Request.Body.Viewer)
	}
	// The response body gets a viewer via the capture snapshot.
	if resp.Entry.Response.Body.Viewer == nil || resp.Entry.Response.Body.Viewer.Kind != "json" {
		t.Fatalf("response viewer = %+v", resp.Entry.Response.Body.Viewer)
	}
}

func TestRepeatRejectsInitialPrivateURL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	mgr, err := NewManager(config.DeveloperConfig{Enabled: true, CaptureLimit: 10})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.store.Add(Entry{
		ID:     "dev-1",
		Method: http.MethodGet,
		URL:    srv.URL + "/start",
		Request: Message{
			Body: Body{Preview: "", Size: 0},
		},
	})

	repeatHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			hits.Add(1)
			return nil, errors.New("transport should not be called for private URL")
		}),
	}
	defer func() { repeatHTTPClient = nil }()

	_, err = mgr.Repeat(context.Background(), RepeatRequest{EntryID: "dev-1"})
	if err == nil {
		t.Fatal("Repeat allowed private initial URL, want error")
	}
	if !strings.Contains(err.Error(), "not public") {
		t.Fatalf("error = %v, want SSRF rejection", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("transport reached %d times, want 0", hits.Load())
	}
}

func TestRepeatRejectsRedirectToPrivate(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{Enabled: true, CaptureLimit: 10})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.store.Add(Entry{
		ID:     "dev-1",
		Method: http.MethodGet,
		URL:    "http://" + publicTestIP + ":9/start",
		Request: Message{
			Body: Body{Preview: "", Size: 0},
		},
	})

	var called []string
	repeatHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			called = append(called, req.URL.Host)
			if req.URL.Path == "/start" {
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     http.Header{"Location": []string{"http://127.0.0.1:9/target"}},
					Body:       io.NopCloser(strings.NewReader("")),
					Request:    req,
				}, nil
			}
			return nil, errors.New("should not reach redirect target")
		}),
	}
	defer func() { repeatHTTPClient = nil }()

	_, err = mgr.Repeat(context.Background(), RepeatRequest{EntryID: "dev-1"})
	if err != nil {
		t.Fatalf("Repeat returned error for redirect rejection: %v", err)
	}
	entries := mgr.store.List(1)
	if len(entries) != 1 {
		t.Fatalf("expected one captured entry, got %+v", entries)
	}
	entry := entries[0]
	if !strings.Contains(entry.Error, "not public") {
		t.Fatalf("entry.Error = %q, want SSRF rejection", entry.Error)
	}
	if len(called) != 1 {
		t.Fatalf("transport called for %v, want only initial request", called)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestBreakpointRequestResolves(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled: true,
		BreakpointRules: []config.DeveloperBreakpointRuleConfig{{
			ID:      "bp",
			Enabled: true,
			Stage:   "request",
			Match: config.DeveloperMatchConfig{
				Host: "example.com",
			},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	errCh := make(chan error, 1)
	go func() {
		deadline := time.After(2 * time.Second)
		for {
			pending := mgr.PendingBreakpoints()
			if len(pending) > 0 {
				if !mgr.ResolveBreakpoint(pending[0].ID, BreakpointResolution{Action: "drop"}) {
					errCh <- err
					return
				}
				errCh <- nil
				return
			}
			select {
			case <-deadline:
				errCh <- context.DeadlineExceeded
				return
			default:
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()
	resolution, paused, err := mgr.BreakpointRequest(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("BreakpointRequest: %v", err)
	}
	if waitErr := <-errCh; waitErr != nil {
		t.Fatal(waitErr)
	}
	if !paused || resolution.Action != "drop" {
		t.Fatalf("paused=%v resolution=%+v", paused, resolution)
	}
}

func TestHasRewriteStageFiltering(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled: true,
		RewriteRules: []config.DeveloperRewriteRuleConfig{{
			ID: "rw", Enabled: true, Stage: "request",
			Match: config.DeveloperMatchConfig{Host: "example.com"},
			Ops:   []config.DeveloperRewriteOp{{Target: "header", Action: "add", Field: "X-A", Value: "1"}},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	if !mgr.HasRequestRewrite(req) {
		t.Error("HasRequestRewrite = false, want true")
	}
	if mgr.HasResponseRewrite(req) {
		t.Error("HasResponseRewrite = true for request-only rule, want false")
	}
}

func TestHasRewriteDisabledRuleSkipped(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled: true,
		RewriteRules: []config.DeveloperRewriteRuleConfig{{
			ID: "rw", Enabled: false, Stage: "both",
			Match: config.DeveloperMatchConfig{Host: "example.com"},
			Ops:   []config.DeveloperRewriteOp{{Target: "header", Action: "add", Field: "X-A", Value: "1"}},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	if mgr.HasRequestRewrite(req) {
		t.Error("HasRequestRewrite = true for disabled rule, want false")
	}
}

func TestRewriteRequestHeadersAndBody(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled: true,
		RewriteRules: []config.DeveloperRewriteRuleConfig{{
			ID: "rw", Enabled: true, Stage: "both",
			Match: config.DeveloperMatchConfig{Host: "example.com"},
			Ops: []config.DeveloperRewriteOp{
				{Target: "header", Action: "add", Field: "X-Added", Value: "yes"},
				{Target: "header", Action: "set", Field: "Accept", Value: "application/json"},
				{Target: "header", Action: "remove", Field: "Cookie"},
				{Target: "body", Action: "replace", Value: "staging", Replace: "production"},
				{Target: "body", Action: "set", Value: `{"x":"new"}`},
			},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api",
		strings.NewReader("hello staging world"))
	req.Header.Set("Cookie", "sensitive=1")
	req.Header.Set("Accept", "text/plain")

	body, err := readRequestBodyForRewrite(req)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	newBody, bodySet, err := mgr.RewriteRequest(req, body)
	if err != nil {
		t.Fatalf("RewriteRequest: %v", err)
	}
	if !bodySet {
		t.Fatal("bodySet = false, want true")
	}
	if got := req.Header.Get("X-Added"); got != "yes" {
		t.Errorf("X-Added = %q, want yes", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want application/json", got)
	}
	if got := req.Header.Get("Cookie"); got != "" {
		t.Errorf("Cookie = %q, want removed (empty)", got)
	}
	if string(newBody) != `{"x":"new"}` {
		t.Errorf("newBody = %q, want the body set op result (ops apply in order; set wins last)", newBody)
	}
}

func TestRewriteRequestBodyReplaceWithoutSet(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled: true,
		RewriteRules: []config.DeveloperRewriteRuleConfig{{
			ID: "rw", Enabled: true, Stage: "request",
			Match: config.DeveloperMatchConfig{Host: "example.com"},
			Ops: []config.DeveloperRewriteOp{
				{Target: "body", Action: "replace", Value: "staging", Replace: "production"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api",
		strings.NewReader("hello staging world"))
	body, err := readRequestBodyForRewrite(req)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	newBody, bodySet, err := mgr.RewriteRequest(req, body)
	if err != nil {
		t.Fatalf("RewriteRequest: %v", err)
	}
	if !bodySet || string(newBody) != "hello production world" {
		t.Fatalf("newBody=%q bodySet=%v, want hello production world true", newBody, bodySet)
	}
}

func TestRewriteRequestNoBodyOpReturnsNoDelta(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled: true,
		RewriteRules: []config.DeveloperRewriteRuleConfig{{
			ID: "rw", Enabled: true, Stage: "request",
			Match: config.DeveloperMatchConfig{Host: "example.com"},
			Ops:   []config.DeveloperRewriteOp{{Target: "header", Action: "add", Field: "X-A", Value: "1"}},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	newBody, bodySet, err := mgr.RewriteRequest(req, nil)
	if err != nil {
		t.Fatalf("RewriteRequest: %v", err)
	}
	if bodySet {
		t.Errorf("bodySet = true for header-only rule, want false")
	}
	if newBody != nil {
		t.Errorf("newBody = %v, want nil", newBody)
	}
	if got := req.Header.Get("X-A"); got != "1" {
		t.Errorf("X-A = %q, want 1", got)
	}
}

func TestRewriteResponseStatusHeaderBody(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled: true,
		RewriteRules: []config.DeveloperRewriteRuleConfig{{
			ID: "rw", Enabled: true, Stage: "both",
			Match: config.DeveloperMatchConfig{Host: "example.com"},
			Ops: []config.DeveloperRewriteOp{
				{Target: "status", Action: "set", Value: "204"},
				{Target: "header", Action: "set", Field: "X-Rewritten", Value: "yes"},
				{Target: "body", Action: "replace", Value: "old", Replace: "new"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	resp := &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("old body old")),
		Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
	}
	resp.Header.Set("X-Rewritten", "no")
	newBody, bodySet, err := mgr.RewriteResponse(req, resp, []byte("old body old"))
	if err != nil {
		t.Fatalf("RewriteResponse: %v", err)
	}
	if resp.StatusCode != 204 || resp.Status != "204 No Content" {
		t.Errorf("status = %d %q, want 204 204 No Content", resp.StatusCode, resp.Status)
	}
	if got := resp.Header.Get("X-Rewritten"); got != "yes" {
		t.Errorf("X-Rewritten = %q, want yes", got)
	}
	if !bodySet || string(newBody) != "new body new" {
		t.Fatalf("newBody=%q bodySet=%v, want new body new true", newBody, bodySet)
	}
}

func TestRewriteRequestStageSkipsResponseRule(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled: true,
		RewriteRules: []config.DeveloperRewriteRuleConfig{{
			ID: "rw", Enabled: true, Stage: "response",
			Match: config.DeveloperMatchConfig{Host: "example.com"},
			Ops:   []config.DeveloperRewriteOp{{Target: "header", Action: "add", Field: "X-A", Value: "1"}},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	if mgr.HasRequestRewrite(req) {
		t.Error("HasRequestRewrite = true for response-only rule, want false")
	}
	newBody, bodySet, err := mgr.RewriteRequest(req, nil)
	if err != nil {
		t.Fatalf("RewriteRequest: %v", err)
	}
	if bodySet {
		t.Error("bodySet = true for response-only rule on request, want false")
	}
	if newBody != nil {
		t.Errorf("newBody = %v, want nil", newBody)
	}
	if got := req.Header.Get("X-A"); got != "" {
		t.Errorf("X-A = %q, want empty (response-only rule must not touch request)", got)
	}
}

func TestRewriteRequestDoesNotMatch(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled: true,
		RewriteRules: []config.DeveloperRewriteRuleConfig{{
			ID: "rw", Enabled: true, Stage: "both",
			Match: config.DeveloperMatchConfig{Host: "other.com"},
			Ops:   []config.DeveloperRewriteOp{{Target: "header", Action: "add", Field: "X-A", Value: "1"}},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	if mgr.HasRequestRewrite(req) {
		t.Error("HasRequestRewrite = true for non-matching host, want false")
	}
}

// readRequestBodyForRewrite reads and restores req.Body so it can be passed to
// RewriteRequest, mirroring what the listener does with readAndResetRequestBody.
func readRequestBodyForRewrite(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	defer req.Body.Close()
	return io.ReadAll(req.Body)
}
