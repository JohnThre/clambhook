package tor

import (
	"errors"
	"fmt"
	"net"

	"github.com/clambhook/clambhook/internal/protocol"
)

type config struct {
	socksAddr     string // host:port of the Tor SOCKS5 port
	isolationUser string // empty means "no user/pass"; both must be set together
	isolationPass string
}

// parseConfig reads a tor server block. server.Address is the tor SOCKS5
// port (most commonly 127.0.0.1:9050 for a locally-run tor); there is no
// separate socks_addr setting because the "address" field already serves
// that role for every other protocol in this codebase.
//
// isolation_user / isolation_pass are optional. When set, they are sent
// as RFC 1929 credentials — tor doesn't authenticate them, it uses the
// pair as a stream-isolation token so each unique pair maps to its own
// circuit. This is how a caller can demand "give this flow a fresh exit".
func parseConfig(s protocol.Server) (config, error) {
	var c config

	if s.Address == "" {
		return c, errors.New("tor: address is required (tor SOCKS5 port, e.g. 127.0.0.1:9050)")
	}
	if _, _, err := net.SplitHostPort(s.Address); err != nil {
		return c, fmt.Errorf("tor: invalid address %q: %w", s.Address, err)
	}
	c.socksAddr = s.Address

	user, _ := s.Settings["isolation_user"].(string)
	pass, _ := s.Settings["isolation_pass"].(string)
	// Either both or neither. A half-set pair almost certainly means the
	// user mistyped a key and would silently get shared-circuit behaviour,
	// which is exactly the opposite of what they wanted when they put
	// something in the isolation field.
	if (user == "") != (pass == "") {
		return c, errors.New("tor: isolation_user and isolation_pass must be set together")
	}
	c.isolationUser = user
	c.isolationPass = pass

	return c, nil
}
