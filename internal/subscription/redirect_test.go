package subscription

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
)

// publicTestIP is a well-known public address used in place of a local test
// server so the SSRF policy permits the request. The test transport dials the
// actual local listener regardless of the URL host.
const publicTestIP = "93.184.216.34"

func publicHostURL(srv *httptest.Server, path string) string {
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return "http://" + publicTestIP + ":" + port + path
}

func publicHostClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
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

// fakeResolver returns fixed addresses for every host, enabling deterministic
// tests of the resolved-private-host path.
type fakeResolver struct {
	addrs []netip.Addr
	err   error
}

func (f fakeResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]netip.Addr(nil), f.addrs...), nil
}

func swapResolver(r ipResolver) func() {
	old := resolver
	resolver = r
	return func() { resolver = old }
}

func TestValidatePublicRedirectHostRejectsNonPublic(t *testing.T) {
	cases := []struct {
		name string
		host string
	}{
		{"loopback ipv4", "127.0.0.1"},
		{"loopback ipv6", "::1"},
		{"localhost name", "localhost"},
		{"localhost suffix", "api.localhost"},
		{"unspecified", "0.0.0.0"},
		{"private 10", "10.0.0.1"},
		{"private 192", "192.168.1.1"},
		{"private 172", "172.16.5.5"},
		{"link-local", "169.254.169.254"},
		{"link-local ipv6", "fe80::1"},
		{"cgnat", "100.64.0.1"},
		{"aws metadata ip", "169.254.169.254"},
		{"gcp metadata host", "metadata.google.internal"},
		{"bare metadata host", "metadata"},
		{"alibaba metadata ip", "100.100.100.200"},
		{"openstack metadata ip", "192.0.0.192"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validatePublicRedirectHost(context.Background(), tc.host); err == nil {
				t.Fatalf("validatePublicRedirectHost(%q) = nil, want rejection", tc.host)
			}
		})
	}
}

func TestValidatePublicRedirectHostAllowsPublicLiteral(t *testing.T) {
	if err := validatePublicRedirectHost(context.Background(), "93.184.216.34"); err != nil {
		t.Fatalf("public literal rejected: %v", err)
	}
}

func TestValidateRedirectURLRejectsUnsafeSchemesAndHosts(t *testing.T) {
	for _, raw := range []string{"ftp://example.com/x", "file:///etc/passwd", "http:///nohost", "gopher://example.com"} {
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			// url.Parse via NewRequest rejects some outright; that is also a rejection.
			continue
		}
		if err := validateRedirectURL(req.URL); err == nil {
			t.Fatalf("validateRedirectURL(%q) = nil, want rejection", raw)
		}
	}
}

func TestSafeRedirectAllowsSameOrigin(t *testing.T) {
	var finalHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			finalHits.Add(1)
			_, _ = w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	resp, err := ClientWithSafeRedirects(srv.Client()).Get(srv.URL + "/start")
	if err != nil {
		t.Fatalf("same-origin redirect failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if finalHits.Load() != 1 {
		t.Fatalf("final endpoint hits = %d, want 1", finalHits.Load())
	}
}

func TestSafeRedirectRejectsUnsafeTargetsBeforeReachingThem(t *testing.T) {
	var targetHits atomic.Int32
	// This server stands in for the redirect target. Rejections must happen
	// before any request reaches it.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte("SHOULD NOT BE FETCHED"))
	}))
	defer target.Close()

	cases := []struct {
		name     string
		location string
		wantErr  string
	}{
		{"loopback target server", target.URL, "not public"},
		{"loopback alt port", "http://127.0.0.1:9/x", "not public"},
		{"localhost", "http://localhost:9/x", "not public"},
		{"private", "http://10.0.0.1/x", "not public"},
		{"link-local metadata", "http://169.254.169.254/latest/meta-data/", "not public"},
		{"metadata host", "http://metadata.google.internal/computeMetadata/", "not public"},
		{"unspecified", "http://0.0.0.0/x", "not public"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			targetHits.Store(0)
			redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, tc.location, http.StatusFound)
			}))
			defer redirector.Close()

			resp, err := ClientWithSafeRedirects(redirector.Client()).Get(redirector.URL)
			if resp != nil {
				resp.Body.Close()
			}
			if err == nil {
				t.Fatalf("redirect to %q allowed, want rejection", tc.location)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
			if targetHits.Load() != 0 {
				t.Fatalf("target reached %d times, want 0", targetHits.Load())
			}
		})
	}
}

func TestRefreshOneFollowsSameOriginRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/list" {
			http.Redirect(w, r, "/list/v2", http.StatusMovedPermanently)
			return
		}
		w.Header().Set("ETag", `"v2"`)
		_, _ = w.Write([]byte("ads.example.com\n"))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "clambhook.toml")
	sub := config.RuleSubscriptionConfig{Name: "ads", URL: publicHostURL(srv, "/list")}
	if err := RefreshOne(context.Background(), path, "default", sub, publicHostClient(t, srv)); err != nil {
		t.Fatalf("RefreshOne: %v", err)
	}
	cache, err := LoadCache(path, "default", sub)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if len(cache.DomainSuffixes) != 1 || cache.DomainSuffixes[0] != "ads.example.com" {
		t.Fatalf("cache domains = %#v", cache.DomainSuffixes)
	}
}

func TestRefreshOneRejectsRedirectToMetadata(t *testing.T) {
	defer swapResolver(fakeResolver{addrs: []netip.Addr{netip.MustParseAddr(publicTestIP)}})()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "clambhook.toml")
	sub := config.RuleSubscriptionConfig{Name: "ads", URL: publicHostURL(srv, "/")}
	err := RefreshOne(context.Background(), path, "default", sub, publicHostClient(t, srv))
	if err == nil {
		t.Fatal("RefreshOne followed metadata redirect, want error")
	}
	if !strings.Contains(err.Error(), "not public") {
		t.Fatalf("error = %v, want redirect rejection", err)
	}
	if _, err := LoadCache(path, "default", sub); err == nil {
		t.Fatal("cache written despite rejected redirect")
	}
}

func TestRefreshOnePreservesConditionalGET(t *testing.T) {
	defer swapResolver(fakeResolver{addrs: []netip.Addr{netip.MustParseAddr(publicTestIP)}})()

	var conditional atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inm := r.Header.Get("If-None-Match"); inm != "" {
			conditional.Store(true)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte("ads.example.com\n"))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "clambhook.toml")
	sub := config.RuleSubscriptionConfig{Name: "ads", URL: publicHostURL(srv, "/")}
	if err := RefreshOne(context.Background(), path, "default", sub, publicHostClient(t, srv)); err != nil {
		t.Fatalf("first RefreshOne: %v", err)
	}
	if err := RefreshOne(context.Background(), path, "default", sub, publicHostClient(t, srv)); err != nil {
		t.Fatalf("second RefreshOne: %v", err)
	}
	if !conditional.Load() {
		t.Fatal("conditional If-None-Match header was not sent through the wrapped client")
	}
	cache, err := LoadCache(path, "default", sub)
	if err != nil {
		t.Fatalf("LoadCache after 304: %v", err)
	}
	if len(cache.DomainSuffixes) != 1 {
		t.Fatalf("cache after 304 lost data: %#v", cache.DomainSuffixes)
	}
}

func TestRefreshOneRejectsInitialPrivateURL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("ads.example.com\n"))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "clambhook.toml")
	sub := config.RuleSubscriptionConfig{Name: "ads", URL: srv.URL + "/list"}
	err := RefreshOne(context.Background(), path, "default", sub, srv.Client())
	if err == nil {
		t.Fatal("RefreshOne fetched private URL, want error")
	}
	if !strings.Contains(err.Error(), "not public") {
		t.Fatalf("error = %v, want SSRF rejection", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("server reached %d times, want 0", hits.Load())
	}
	if _, err := LoadCache(path, "default", sub); err == nil {
		t.Fatal("cache written despite rejected URL")
	}
}

func hostnameURL(host string, srv *httptest.Server, path string) string {
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return "http://" + host + ":" + port + path
}

func TestRefreshOneRejectsInitialResolvedPrivateHost(t *testing.T) {
	defer swapResolver(fakeResolver{addrs: []netip.Addr{netip.MustParseAddr("127.0.0.1")}})()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("ads.example.com\n"))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "clambhook.toml")
	sub := config.RuleSubscriptionConfig{Name: "ads", URL: hostnameURL("public.example", srv, "/list")}
	err := RefreshOne(context.Background(), path, "default", sub, publicHostClient(t, srv))
	if err == nil {
		t.Fatal("RefreshOne allowed public hostname resolving to private, want error")
	}
	if !strings.Contains(err.Error(), "not public") && !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("error = %v, want SSRF rejection", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("server reached %d times, want 0", hits.Load())
	}
}

func TestRefreshOneRejectsRedirectToResolvedPrivateHost(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "http://private.example/target", http.StatusFound)
			return
		}
		hits.Add(1)
		_, _ = w.Write([]byte("ads.example.com\n"))
	}))
	defer srv.Close()

	// Allow the initial hostname, then make the cross-origin redirect host
	// resolve to a private address.
	defer swapResolver(fakeResolver{addrs: []netip.Addr{netip.MustParseAddr(publicTestIP)}})()
	resolver = fakeResolver{addrs: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}

	path := filepath.Join(t.TempDir(), "clambhook.toml")
	sub := config.RuleSubscriptionConfig{Name: "ads", URL: hostnameURL("public.example", srv, "/start")}
	err := RefreshOne(context.Background(), path, "default", sub, publicHostClient(t, srv))
	if err == nil {
		t.Fatal("RefreshOne followed redirect to resolved-private host, want error")
	}
	if !strings.Contains(err.Error(), "not public") && !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("error = %v, want SSRF rejection", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("target reached %d times, want 0", hits.Load())
	}
	if _, err := LoadCache(path, "default", sub); err == nil {
		t.Fatal("cache written despite rejected redirect")
	}
}
