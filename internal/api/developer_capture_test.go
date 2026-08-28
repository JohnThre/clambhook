// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/developer"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/listener"
	_ "github.com/JohnThre/clambhook/internal/protocol/shadowsocks"
)

func newDeveloperCaptureServer(t *testing.T) (*Server, *developer.Manager) {
	t.Helper()
	cfg := testDeveloperSettingsConfig(t)
	cfg.Developer.Enabled = true
	cfg.Developer.MITMEnabled = false
	cfg.Developer.BodyLimitBytes = 1 << 14
	dev, err := developer.NewManager(cfg.Developer)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return NewWithOptions(engine.New(cfg, nil), nil, Options{Developer: dev}), dev
}

func seedCaptureEntry(t *testing.T, dev *developer.Manager, method, url string, status int, reqBody, respBody, respMime string) string {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	if reqBody != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Seed", "marker")
	tx := dev.Begin(context.Background(), listener.HTTPCaptureMeta{Scheme: schemeOf(url), Target: hostOf(url), StartedAt: time.Now()}, req)
	if tx == nil {
		t.Fatal("nil inspection")
	}
	if req.Body != nil {
		req.Body = tx.RequestBody(req.Body)
		_, _ = io.Copy(io.Discard, req.Body)
	}
	resp := &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(respBody))}
	if respMime != "" {
		resp.Header.Set("Content-Type", respMime)
	}
	resp.Body = tx.ResponseBody(resp.Body)
	_, _ = io.Copy(io.Discard, resp.Body)
	tx.Finish(resp, nil)
	entries := dev.List(0)
	if len(entries) == 0 {
		t.Fatal("no entries after seed")
	}
	return entries[0].ID
}

func seedCaptureEntryWithTimings(t *testing.T, dev *developer.Manager, url string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Seed", "marker")
	tx := dev.Begin(context.Background(), listener.HTTPCaptureMeta{Scheme: schemeOf(url), Target: hostOf(url), StartedAt: time.Now()}, req)
	if tx == nil {
		t.Fatal("nil inspection")
	}
	st, ok := tx.(listener.HTTPInspectionTimings)
	if !ok {
		t.Fatal("inspection does not implement HTTPInspectionTimings")
	}
	st.SetTimings(listener.HTTPTimings{
		Connect: 3 * time.Millisecond,
		SSL:     7 * time.Millisecond,
		Send:    1 * time.Millisecond,
		Wait:    40 * time.Millisecond,
		Receive: 12 * time.Millisecond,
	})
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}
	resp.Body = tx.ResponseBody(resp.Body)
	_, _ = io.Copy(io.Discard, resp.Body)
	tx.Finish(resp, nil)
	entries := dev.List(0)
	if len(entries) == 0 {
		t.Fatal("no entries after seed")
	}
	return entries[0].ID
}

func TestDeveloperEntriesFilterParams(t *testing.T) {
	srv, dev := newDeveloperCaptureServer(t)
	seedCaptureEntry(t, dev, "GET", "https://api.example.com/v1/users", 200, "", `[]`, "application/json")
	seedCaptureEntry(t, dev, "POST", "https://api.example.com/v1/items", 201, "", `created`, "text/plain")
	seedCaptureEntry(t, dev, "GET", "https://err.example.com/v1/boom", 500, "", `oops`, "text/plain")

	cases := []struct {
		name   string
		query  string
		want   int
		wantID string
	}{
		{"no filter", "", 3, ""},
		{"method", "?method=POST", 1, ""},
		{"method multi", "?method=GET,POST", 3, ""},
		{"status_min", "?status_min=400", 1, ""},
		{"scheme https", "?scheme=https", 3, ""},
		{"host substring", "?host=err.", 1, ""},
		{"content_type", "?content_type=json", 1, ""},
		{"query url", "?q=/v1/items", 1, ""},
		{"query header", "?q=X-Seed:marker", 3, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/entries"+tc.query, nil)
			rec := httptest.NewRecorder()
			srv.server.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
			}
			var resp struct {
				Entries []developer.Entry `json:"entries"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if len(resp.Entries) != tc.want {
				t.Fatalf("got %d entries, want %d", len(resp.Entries), tc.want)
			}
			if tc.wantID != "" && (len(resp.Entries) == 0 || resp.Entries[0].ID != tc.wantID) {
				t.Fatalf("first = %+v, want %s", resp.Entries, tc.wantID)
			}
		})
	}
}

func TestDeveloperEntriesFilterRejectsBadParams(t *testing.T) {
	srv, _ := newDeveloperCaptureServer(t)
	cases := []string{
		"?limit=abc",
		"?status_min=abc",
		"?status_max=-5",
		"?error_only=maybe",
	}
	for _, q := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/entries"+q, nil)
		rec := httptest.NewRecorder()
		srv.server.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("query %q: status = %d, want 400 (body=%q)", q, rec.Code, rec.Body.String())
		}
	}
}

func TestDeveloperEntriesCarryTimingsAndViewer(t *testing.T) {
	srv, dev := newDeveloperCaptureServer(t)
	id := seedCaptureEntryWithTimings(t, dev, "https://api.example.com/v1/users")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/entries", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	var resp struct {
		Entries []developer.Entry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range resp.Entries {
		if e.ID == id {
			found = true
			if e.Timings == nil || e.Timings.Wait != 40 {
				t.Fatalf("timings not surfaced: %+v", e.Timings)
			}
			if e.Response.Body.Viewer == nil || e.Response.Body.Viewer.Kind != "json" {
				t.Fatalf("viewer not surfaced: %+v", e.Response.Body.Viewer)
			}
		}
	}
	if !found {
		t.Fatal("seeded entry missing from list")
	}
}

func TestDeveloperEntryCurlExport(t *testing.T) {
	srv, dev := newDeveloperCaptureServer(t)
	id := seedCaptureEntry(t, dev, "POST", "https://api.example.com/v1/items", 201, `{"a":1}`, `created`, "text/plain")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/entries/"+id+"/curl", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
	var resp struct {
		Curl string `json:"curl"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Curl, "curl -X 'POST'") {
		t.Fatalf("curl = %q", resp.Curl)
	}
	if !strings.Contains(resp.Curl, "--data-raw '{\"a\":1}'") {
		t.Fatalf("curl missing body: %q", resp.Curl)
	}
}

func TestDeveloperEntryCurlExportNotFound(t *testing.T) {
	srv, _ := newDeveloperCaptureServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/entries/dev-nope/curl", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDeveloperCurlImport(t *testing.T) {
	srv, _ := newDeveloperCaptureServer(t)
	body, _ := json.Marshal(map[string]string{"curl": `curl https://api.example.com -H 'X: y' -d '{"a":1}'`})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/developer/curl/import", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
	var parsed developer.ParsedCurl
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Method != "POST" || parsed.URL != "https://api.example.com" || parsed.Body != `{"a":1}` {
		t.Fatalf("parsed = %+v", parsed)
	}
	if len(parsed.Headers) != 1 || parsed.Headers[0].Name != "X" {
		t.Fatalf("headers = %+v", parsed.Headers)
	}
}

func TestDeveloperCurlImportErrors(t *testing.T) {
	srv, _ := newDeveloperCaptureServer(t)
	cases := []struct {
		name string
		body string
	}{
		{"no url", `{"curl":"curl -H 'X: y'"}`},
		{"unterminated quote", `{"curl":"curl -H 'X: y"}`},
		{"empty", `{"curl":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/developer/curl/import", bytes.NewReader([]byte(tc.body)))
			rec := httptest.NewRecorder()
			srv.server.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%q)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDeveloperHARTimingsPopulated(t *testing.T) {
	srv, dev := newDeveloperCaptureServer(t)
	seedCaptureEntryWithTimings(t, dev, "https://api.example.com/v1/users")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/har", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	entries := doc["log"].(map[string]any)["entries"].([]any)
	var sawTimings bool
	for _, e := range entries {
		row := e.(map[string]any)
		timings, ok := row["timings"].(map[string]any)
		if !ok {
			continue
		}
		if wait, ok := timings["wait"].(float64); ok && wait == 40 {
			if timings["blocked"] == -1.0 && timings["connect"] == 3.0 {
				sawTimings = true
			}
		}
	}
	if !sawTimings {
		t.Fatal("HAR timings not populated from Entry.Timings")
	}
}

func TestDeveloperSendRejectsBadInput(t *testing.T) {
	srv, _ := newDeveloperCaptureServer(t)
	cases := []struct {
		name string
		body string
	}{
		{"bad json", `{not json`},
		{"empty url", `{"method":"GET","url":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/developer/send", bytes.NewReader([]byte(tc.body)))
			rec := httptest.NewRecorder()
			srv.server.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%q)", rec.Code, rec.Body.String())
			}
		})
	}
}

func schemeOf(u string) string {
	if strings.HasPrefix(u, "https://") {
		return "https"
	}
	return "http"
}

func hostOf(u string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i]
	}
	return s
}
