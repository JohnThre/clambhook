package api

import (
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/JohnThre/clambhook/internal/developer"
	"github.com/JohnThre/clambhook/internal/traffic"
)

// Options controls optional API server behavior.
type Options struct {
	// AuthToken enables bearer-token authentication for all API routes when
	// non-empty. The token is intentionally opaque; callers own generation and
	// storage.
	AuthToken string

	// TrafficStore enables the /api/v1/traffic snapshot endpoint when non-nil.
	TrafficStore *traffic.Store

	// Developer enables opt-in developer-mode inspector endpoints.
	Developer *developer.Manager

	// ConfigPath enables API routes that persist changes to the daemon config.
	ConfigPath string
}

// ValidateAuthConfig rejects exposing an unauthenticated control API on
// non-loopback interfaces.
func ValidateAuthConfig(addr, token string) error {
	if strings.TrimSpace(token) != "" {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return errors.New("api token is required when binding the API to a non-loopback address")
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	token := s.authToken
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !bearerTokenMatches(r.Header.Get("Authorization"), token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="clambhook"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerTokenMatches(header, token string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got := strings.TrimPrefix(header, prefix)
	if len(got) != len(token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}
