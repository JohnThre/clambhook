package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// loopbackWSOriginPatterns lists the Origin host patterns the /api/v1/events
// WebSocket endpoint authorizes in addition to the library's same-origin
// default. guardMiddleware has already rejected any non-loopback, non-bind
// Origin before a request reaches websocket.Accept, so these only keep local
// cross-port browser clients (e.g. a page on 127.0.0.1:8080 talking to the
// daemon on 127.0.0.1:9090) working without the insecure origin bypass.
var loopbackWSOriginPatterns = []string{
	"localhost", "localhost:*",
	"127.0.0.1", "127.0.0.1:*",
	"[::1]", "[::1]:*",
}

// guardMiddleware protects the control API from local browsers. It enforces two
// independent checks before any route (including the WebSocket upgrade) runs:
//
//   - Host / DNS-rebinding: when the bound address is known, the request Host
//     must use the bound port and either the configured host or a loopback name.
//     This defeats DNS rebinding, where a hostile page reaches the daemon via a
//     name it controls (Host: evil.com).
//   - Origin / cross-origin: when an Origin header is present it must be a
//     loopback origin (any port) or the configured bind host. Browsers attach
//     Origin to cross-origin fetch/XHR and state-changing requests, so this
//     blocks hostile web pages while keeping local browser clients working.
//
// Native clients (Apple, Android, TUI) send no Origin and connect on loopback,
// so they are unaffected. The checks run ahead of authMiddleware so a hostile
// origin is rejected regardless of the bearer-token policy.
func (s *Server) guardMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.originAndHostAllowed(r) {
			http.Error(w, "forbidden: cross-origin or untrusted host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) originAndHostAllowed(r *http.Request) bool {
	bindHost, bindPort, bindKnown := s.bindAuthority()

	if bindKnown && !bindIsWildcard(bindHost) {
		if !hostHeaderAllowed(r.Host, bindHost, bindPort) {
			return false
		}
	}

	if origin := r.Header.Get("Origin"); origin != "" {
		return originAllowed(origin, bindHost, bindKnown)
	}
	return true
}

// bindAuthority returns the host and port of the address the API is bound to.
// Before Start the address is unknown, in which case callers skip the Host
// check; Origin validation remains active because it uses the request target.
func (s *Server) bindAuthority() (host, port string, known bool) {
	addr := s.Addr()
	if addr == "" {
		return "", "", false
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", false
	}
	return strings.Trim(host, "[]"), port, true
}

// hostHeaderAllowed reports whether the request Host uses the bound port and a
// loopback name or the configured bind host.
func hostHeaderAllowed(hostHeader, bindHost, bindPort string) bool {
	hn, port, ok := splitAuthority(hostHeader, "http")
	if !ok || port != bindPort {
		return false
	}
	if isLoopbackName(hn) {
		return true
	}
	return strings.EqualFold(hn, bindHost)
}

// originAllowed reports whether an Origin header identifies a loopback origin or
// the configured bind host. Opaque and malformed origins (including "null") have
// no usable host and are rejected, as are non-http(s) schemes.
func originAllowed(origin, bindHost string, bindKnown bool) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	hn := u.Hostname()
	if hn == "" {
		return false
	}
	if isLoopbackName(hn) {
		return true
	}
	return bindKnown && strings.EqualFold(hn, bindHost)
}

func splitAuthority(authority, scheme string) (host, port string, ok bool) {
	authority = strings.TrimSpace(authority)
	if authority == "" {
		return "", "", false
	}
	if h, p, err := net.SplitHostPort(authority); err == nil {
		return strings.Trim(h, "[]"), p, true
	}
	if strings.Contains(authority, ":") {
		return "", "", false
	}
	port = "80"
	if strings.EqualFold(scheme, "https") {
		port = "443"
	}
	return authority, port, true
}

func isLoopbackName(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// bindIsWildcard reports whether the bind host is an unspecified address, for
// which the reachable Host cannot be determined. Such binds require a bearer
// token (see ValidateAuthConfig), so the Host check is skipped and the token
// authenticates callers instead.
func bindIsWildcard(bindHost string) bool {
	if bindHost == "" {
		return true
	}
	if ip := net.ParseIP(bindHost); ip != nil {
		return ip.IsUnspecified()
	}
	return false
}
