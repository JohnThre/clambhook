package developer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
)

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
		URL:    server.URL,
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
