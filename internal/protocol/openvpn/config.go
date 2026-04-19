package openvpn

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/clambhook/clambhook/internal/protocol"
)

// config is the internal, validated form of an OpenVPN TOML settings
// block. parseConfig builds it; the dialer holds it for the lifetime of
// the daemon process. PEM blocks are pre-parsed into their Go crypto
// forms so bring-up doesn't need to re-parse on every dial.
type config struct {
	remote      string
	caPool      *x509.CertPool
	clientCert  tls.Certificate
	serverCN    string // empty = don't pin
	skipVerify  bool
	username    string
	password    string
	cipher      string // "" = let NCP decide; "AES-256-GCM" | "CHACHA20-POLY1305"
	tunMTU      int
}

// supportedCiphers is the set of AEAD ciphers we can actually speak.
// Advertised to the server via IV_CIPHERS; we reject a PUSH_REPLY that
// names anything else. CBC+HMAC is deliberately omitted — OpenVPN 2.6
// deprecated it and most servers run AEAD by default now.
var supportedCiphers = []string{"AES-256-GCM", "CHACHA20-POLY1305"}

func parseConfig(s protocol.Server) (*config, error) {
	c := &config{tunMTU: 1500}

	if s.Address == "" {
		return nil, errors.New("openvpn: address is required (vpn server host:port)")
	}
	if _, _, err := net.SplitHostPort(s.Address); err != nil {
		return nil, fmt.Errorf("openvpn: invalid address %q: %w", s.Address, err)
	}
	c.remote = s.Address

	caPEM, _ := s.Settings["ca_cert"].(string)
	if strings.TrimSpace(caPEM) == "" {
		return nil, errors.New("openvpn: ca_cert is required (PEM-encoded server CA)")
	}
	c.caPool = x509.NewCertPool()
	if !c.caPool.AppendCertsFromPEM([]byte(caPEM)) {
		return nil, errors.New("openvpn: ca_cert did not contain any valid PEM certificates")
	}

	certPEM, _ := s.Settings["client_cert"].(string)
	keyPEM, _ := s.Settings["client_key"].(string)
	if strings.TrimSpace(certPEM) == "" || strings.TrimSpace(keyPEM) == "" {
		return nil, errors.New("openvpn: client_cert and client_key are required (PEM)")
	}
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, fmt.Errorf("openvpn: load client cert/key: %w", err)
	}
	c.clientCert = cert

	if v, ok := s.Settings["server_cn"].(string); ok {
		c.serverCN = v
	}
	if v, ok := s.Settings["skip_cert_verify"].(bool); ok {
		c.skipVerify = v
	}
	if v, ok := s.Settings["username"].(string); ok {
		c.username = v
	}
	if v, ok := s.Settings["password"].(string); ok {
		c.password = v
	}
	// Either both or neither — matches the pattern used for tor's isolation
	// creds. Half-set credentials would fail the server's --auth-user-pass
	// check in a confusing way ("password mismatch" with no username).
	if (c.username == "") != (c.password == "") {
		return nil, errors.New("openvpn: username and password must be set together")
	}

	if v, ok := s.Settings["cipher"].(string); ok && v != "" {
		upper := strings.ToUpper(v)
		found := false
		for _, ok := range supportedCiphers {
			if upper == ok {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("openvpn: unsupported cipher %q (supported: %s)",
				v, strings.Join(supportedCiphers, ", "))
		}
		c.cipher = upper
	}

	if v, ok := s.Settings["tun_mtu"].(int64); ok && v > 0 {
		c.tunMTU = int(v)
	}

	return c, nil
}
