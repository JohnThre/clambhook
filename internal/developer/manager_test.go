package developer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/listener"
)

func TestCaptureRedactsAndTruncates(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled:               true,
		MITMEnabled:           false,
		CaptureLimit:          10,
		BodyLimitBytes:        4,
		HeaderValueLimitBytes: 5,
		RedactHeaders:         []string{"authorization"},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://example.com/upload?access_token=secret&keep=ok", io.NopCloser(strings.NewReader("abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "secret")
	req.Header.Set("X-Long", "123456789")
	tx := mgr.Begin(context.Background(), listener.HTTPCaptureMeta{Scheme: "http", Target: "example.com:80"}, req)
	req.Body = tx.RequestBody(req.Body)
	if _, err := io.Copy(io.Discard, req.Body); err != nil {
		t.Fatal(err)
	}
	tx.Finish(&http.Response{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Set-Cookie": []string{"a=b"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil)

	entries := mgr.List(0)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	gotURL, err := url.Parse(entry.URL)
	if err != nil {
		t.Fatalf("parse captured URL: %v", err)
	}
	if gotURL.Query().Get("access_token") != redactedValue || gotURL.Query().Get("keep") != "ok" {
		t.Fatalf("captured URL query not redacted: %q", entry.URL)
	}
	if entry.Request.Body.Preview != "abcd" || !entry.Request.Body.Truncated || entry.Request.Body.Size != 6 {
		t.Fatalf("body = %+v", entry.Request.Body)
	}
	if !entry.Request.Headers[0].Redacted || entry.Request.Headers[0].Value != redactedValue {
		t.Fatalf("headers = %+v", entry.Request.Headers)
	}
	foundLong := false
	for _, header := range entry.Request.Headers {
		if header.Name == "X-Long" {
			foundLong = true
			if header.Value != "12345" || !header.Truncated {
				t.Fatalf("X-Long header = %+v", header)
			}
		}
	}
	if !foundLong {
		t.Fatalf("X-Long header missing: %+v", entry.Request.Headers)
	}
	harData, err := json.Marshal(mgr.HAR())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(harData), "access_token=secret") {
		t.Fatalf("HAR leaked query secret: %s", harData)
	}
}

func TestHARSerializesEntries(t *testing.T) {
	store := NewStore(10)
	store.Add(Entry{
		ID:     "dev-1",
		Method: http.MethodGet,
		URL:    "http://example.com/",
		Status: http.StatusOK,
	})
	doc := harDocument(store.List(0))
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal HAR: %v", err)
	}
	if !strings.Contains(string(data), `"version":"1.2"`) || !strings.Contains(string(data), `"url":"http://example.com/"`) {
		t.Fatalf("HAR = %s", data)
	}
}

func TestDeveloperCAGeneration(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled:     true,
		MITMEnabled: true,
		CACertPath:  filepath.Join(dir, "ca.pem"),
		CAKeyPath:   filepath.Join(dir, "ca-key.pem"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if !mgr.MITMEnabled() {
		t.Fatal("MITMEnabled = false, want true")
	}
	if _, err := mgr.TLSConfig("example.com"); err != nil {
		t.Fatalf("TLSConfig: %v", err)
	}
	cert, ok := mgr.CACertPEM()
	if !ok || !strings.Contains(string(cert), "BEGIN CERTIFICATE") {
		t.Fatalf("CA cert unavailable")
	}
	status := mgr.Status()
	if status.CAFingerprintSHA256 == "" || status.CACertPath == "" {
		t.Fatalf("status = %+v", status)
	}
}
