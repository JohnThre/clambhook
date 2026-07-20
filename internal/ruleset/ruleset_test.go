package ruleset

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
)

func TestRefreshOneFollowsSameOriginRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.Redirect(w, r, "/set/v2", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ads.example.com\n"))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "clambhook.toml")
	set := config.RuleSetConfig{Name: "ads", URL: srv.URL + "/set"}
	if err := RefreshOne(context.Background(), path, "default", set, srv.Client()); err != nil {
		t.Fatalf("RefreshOne: %v", err)
	}
	cache, err := LoadCache(path, "default", set)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if len(cache.DomainSuffixes) != 1 || cache.DomainSuffixes[0] != "ads.example.com" {
		t.Fatalf("cache domains = %#v", cache.DomainSuffixes)
	}
}

func TestRefreshOneRejectsUnsafeRedirectsBeforeReachingTarget(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte("ads.example.com\n"))
	}))
	defer target.Close()

	cases := []struct {
		name     string
		location string
	}{
		{"loopback target", target.URL},
		{"localhost", "http://localhost:9/x"},
		{"private", "http://10.0.0.1/x"},
		{"link-local metadata", "http://169.254.169.254/latest/meta-data/"},
		{"metadata host", "http://metadata.google.internal/computeMetadata/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			targetHits.Store(0)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, tc.location, http.StatusFound)
			}))
			defer srv.Close()

			path := filepath.Join(t.TempDir(), "clambhook.toml")
			set := config.RuleSetConfig{Name: "ads", URL: srv.URL}
			err := RefreshOne(context.Background(), path, "default", set, srv.Client())
			if err == nil {
				t.Fatalf("RefreshOne followed redirect to %q, want error", tc.location)
			}
			if !strings.Contains(err.Error(), "not public") {
				t.Fatalf("error = %v, want redirect rejection", err)
			}
			if targetHits.Load() != 0 {
				t.Fatalf("target reached %d times, want 0", targetHits.Load())
			}
			if _, err := LoadCache(path, "default", set); err == nil {
				t.Fatal("cache written despite rejected redirect")
			}
		})
	}
}

func TestRefreshOnePreservesConditionalGET(t *testing.T) {
	var conditional atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			conditional.Store(true)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte("ads.example.com\n"))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "clambhook.toml")
	set := config.RuleSetConfig{Name: "ads", URL: srv.URL}
	if err := RefreshOne(context.Background(), path, "default", set, srv.Client()); err != nil {
		t.Fatalf("first RefreshOne: %v", err)
	}
	if err := RefreshOne(context.Background(), path, "default", set, srv.Client()); err != nil {
		t.Fatalf("second RefreshOne: %v", err)
	}
	if !conditional.Load() {
		t.Fatal("conditional If-None-Match header was not sent through the wrapped client")
	}
	cache, err := LoadCache(path, "default", set)
	if err != nil {
		t.Fatalf("LoadCache after 304: %v", err)
	}
	if len(cache.DomainSuffixes) != 1 {
		t.Fatalf("cache after 304 lost data: %#v", cache.DomainSuffixes)
	}
}
