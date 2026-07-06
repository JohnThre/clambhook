package developer

import (
	"bytes"
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

func TestCaptureCookiesBodyMetadataAndHAR(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled:        true,
		CaptureLimit:   10,
		BodyLimitBytes: 64,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://example.com/api", io.NopCloser(bytes.NewReader([]byte{0xff, 0x00, 0x01})))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Cookie", "session=secret; theme=light")
	tx := mgr.Begin(context.Background(), listener.HTTPCaptureMeta{Scheme: "http", Target: "example.com:80"}, req)
	req.Body = tx.RequestBody(req.Body)
	if _, err := io.Copy(io.Discard, req.Body); err != nil {
		t.Fatal(err)
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"Set-Cookie":   []string{"token=secret; Path=/; HttpOnly; Secure; SameSite=Lax"},
		},
		Body: io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}
	resp.Body = tx.ResponseBody(resp.Body)
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	tx.Finish(resp, nil)

	entry := mgr.List(0)[0]
	if entry.Request.Body.Encoding != "base64" || entry.Request.Body.PreviewBase64 == "" || entry.Request.Body.MimeType != "application/octet-stream" {
		t.Fatalf("request body metadata = %+v", entry.Request.Body)
	}
	if len(entry.Request.Cookies) != 2 || !entry.Request.Cookies[0].Redacted || entry.Request.Cookies[0].Value != redactedValue {
		t.Fatalf("request cookies = %+v", entry.Request.Cookies)
	}
	if len(entry.Response.Cookies) != 1 || !entry.Response.Cookies[0].Redacted || !entry.Response.Cookies[0].HTTPOnly || !entry.Response.Cookies[0].Secure {
		t.Fatalf("response cookies = %+v", entry.Response.Cookies)
	}
	harData, err := json.Marshal(mgr.HAR())
	if err != nil {
		t.Fatal(err)
	}
	harText := string(harData)
	for _, leak := range []string{"session=secret", "token=secret"} {
		if strings.Contains(harText, leak) {
			t.Fatalf("HAR leaked %q: %s", leak, harText)
		}
	}
	for _, want := range []string{`"encoding":"base64"`, `"cookies"`, `"mimeType":"application/octet-stream"`} {
		if !strings.Contains(harText, want) {
			t.Fatalf("HAR missing %q: %s", want, harText)
		}
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
	if status.CAFingerprintSHA256 == "" || status.CACertPath == "" || status.CANotBefore == "" || status.CANotAfter == "" {
		t.Fatalf("status = %+v", status)
	}
	before := status.CAFingerprintSHA256
	regen, err := mgr.RegenerateCA()
	if err != nil {
		t.Fatalf("RegenerateCA: %v", err)
	}
	if regen.CAFingerprintSHA256 == "" || regen.CAFingerprintSHA256 == before {
		t.Fatalf("regenerated status = %+v, old fingerprint %s", regen, before)
	}
}

func TestShouldDecryptHostEmptyAllowlistDecryptsAll(t *testing.T) {
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
	for _, host := range []string{"example.com", "other.test", "sub.example.com"} {
		if !mgr.ShouldDecryptHost(host) {
			t.Fatalf("ShouldDecryptHost(%q) = false, want true with empty allowlist", host)
		}
	}
}

func TestShouldDecryptHostAllowlistRestrictsMatching(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(config.DeveloperConfig{
		Enabled:         true,
		MITMEnabled:     true,
		CACertPath:      filepath.Join(dir, "ca.pem"),
		CAKeyPath:       filepath.Join(dir, "ca-key.pem"),
		SSLDecryptHosts: []string{"example.com", "*.allowed.test"},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	cases := []struct {
		host string
		want bool
	}{
		{"example.com", true},
		{"EXAMPLE.COM", true},
		{"api.allowed.test", true},
		{"deep.api.allowed.test", true}, // "*" absorbs any run of characters, including dots
		{"allowed.test", false},         // missing the required "." before "allowed.test"
		{"other.com", false},
	}
	for _, tc := range cases {
		if got := mgr.ShouldDecryptHost(tc.host); got != tc.want {
			t.Errorf("ShouldDecryptHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestShouldDecryptHostFalseWhenMITMDisabled(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{Enabled: true, MITMEnabled: false})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if mgr.ShouldDecryptHost("example.com") {
		t.Fatal("ShouldDecryptHost = true, want false when MITM disabled")
	}
}
