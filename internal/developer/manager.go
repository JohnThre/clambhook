// Package developer implements the opt-in HTTP(S) debugging inspector.
package developer

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/listener"
)

const redactedValue = "[redacted]"

// Manager owns developer-mode capture state and CA material.
type Manager struct {
	mu          sync.RWMutex
	cfg         config.DeveloperConfig
	store       *Store
	ca          *caMaterial
	certs       map[string]tls.Certificate
	pending     map[string]*pendingBreakpoint
	nextID      atomic.Uint64
	nextPending atomic.Uint64
}

// Status describes developer-mode state for API/TUI display.
type Status struct {
	Enabled               bool   `json:"enabled"`
	MITMEnabled           bool   `json:"mitm_enabled"`
	CaptureLimit          int    `json:"capture_limit"`
	BodyLimitBytes        int64  `json:"body_limit_bytes"`
	HeaderValueLimitBytes int    `json:"header_value_limit_bytes"`
	CACertPath            string `json:"ca_cert_path,omitempty"`
	CAFingerprintSHA256   string `json:"ca_fingerprint_sha256,omitempty"`
	CaptureCount          int    `json:"capture_count"`
}

// NewManager creates a developer manager. Disabled configs keep a nil store
// and do not create CA material.
func NewManager(cfg config.DeveloperConfig) (*Manager, error) {
	cfg = fillConfig(cfg)
	m := &Manager{cfg: cfg, certs: make(map[string]tls.Certificate), pending: make(map[string]*pendingBreakpoint)}
	if cfg.Enabled {
		m.store = NewStore(cfg.CaptureLimit)
		if cfg.MITMEnabled {
			ca, err := loadOrCreateCA(cfg)
			if err != nil {
				return nil, err
			}
			m.ca = ca
		}
	}
	return m, nil
}

// Reconfigure applies developer settings after config reload.
func (m *Manager) Reconfigure(cfg config.DeveloperConfig) error {
	if m == nil {
		return nil
	}
	cfg = fillConfig(cfg)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
	m.certs = make(map[string]tls.Certificate)
	if !cfg.Enabled {
		m.store = nil
		m.ca = nil
		return nil
	}
	if m.store == nil {
		m.store = NewStore(cfg.CaptureLimit)
	} else {
		m.store.Reconfigure(cfg.CaptureLimit)
	}
	if cfg.MITMEnabled {
		ca, err := loadOrCreateCA(cfg)
		if err != nil {
			return err
		}
		m.ca = ca
	} else {
		m.ca = nil
	}
	return nil
}

// Enabled reports whether developer capture is active.
func (m *Manager) Enabled() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Enabled && m.store != nil
}

// MITMEnabled reports whether HTTPS CONNECT should be intercepted.
func (m *Manager) MITMEnabled() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Enabled && m.cfg.MITMEnabled && m.ca != nil
}

// TLSConfig returns a server TLS config for a CONNECT target host.
func (m *Manager) TLSConfig(host string) (*tls.Config, error) {
	cert, err := m.certForHost(host)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"http/1.1"},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// Begin starts a captured HTTP transaction. nil means capture is disabled.
func (m *Manager) Begin(_ context.Context, meta listener.HTTPCaptureMeta, req *http.Request) listener.HTTPInspection {
	if m == nil || req == nil {
		return nil
	}
	m.mu.RLock()
	cfg := m.cfg
	store := m.store
	m.mu.RUnlock()
	if !cfg.Enabled || store == nil {
		return nil
	}
	id := fmt.Sprintf("dev-%d", m.nextID.Add(1))
	started := meta.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	entry := Entry{
		ID:         id,
		ConnID:     meta.ConnID,
		Profile:    meta.Profile,
		ClientAddr: meta.ClientAddr,
		ChainName:  meta.ChainName,
		StartedAt:  started,
		Method:     req.Method,
		URL:        redactCapturedURL(requestURL(meta, req), cfg),
		Scheme:     requestScheme(meta, req),
		Host:       requestHost(meta, req),
		Request: Message{
			Headers: cloneHeaders(req.Header, cfg),
		},
	}
	return &transaction{
		cfg:     cfg,
		store:   store,
		entry:   entry,
		reqBody: newBodyCapture(cfg.BodyLimitBytes),
	}
}

// Status returns a consistent status snapshot.
func (m *Manager) Status() Status {
	if m == nil {
		return Status{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := Status{
		Enabled:               m.cfg.Enabled,
		MITMEnabled:           m.cfg.MITMEnabled && m.ca != nil,
		CaptureLimit:          m.cfg.CaptureLimit,
		BodyLimitBytes:        m.cfg.BodyLimitBytes,
		HeaderValueLimitBytes: m.cfg.HeaderValueLimitBytes,
	}
	if m.ca != nil {
		status.CACertPath = m.ca.certPath
		status.CAFingerprintSHA256 = fingerprint(m.ca.cert.Raw)
	}
	if m.store != nil {
		status.CaptureCount = len(m.store.List(0))
	}
	return status
}

// ConfigSnapshot returns the current developer configuration.
func (m *Manager) ConfigSnapshot() config.DeveloperConfig {
	if m == nil {
		return config.DeveloperConfig{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// CACertPEM returns the CA certificate PEM bytes.
func (m *Manager) CACertPEM() ([]byte, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.ca == nil {
		return nil, false
	}
	return append([]byte(nil), m.ca.certPEM...), true
}

// List returns captured entries, newest first.
func (m *Manager) List(limit int) []Entry {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return []Entry{}
	}
	return store.List(limit)
}

// Get returns a captured entry by id.
func (m *Manager) Get(id string) (Entry, bool) {
	if m == nil {
		return Entry{}, false
	}
	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return Entry{}, false
	}
	return store.Get(id)
}

// Clear removes all captured entries.
func (m *Manager) Clear() {
	if m == nil {
		return
	}
	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store != nil {
		store.Clear()
	}
}

// HAR serializes captured entries as HAR 1.2 JSON-compatible data.
func (m *Manager) HAR() map[string]any {
	if m == nil {
		return harDocument(nil)
	}
	return harDocument(m.List(0))
}

type transaction struct {
	cfg      config.DeveloperConfig
	store    *Store
	entry    Entry
	reqBody  *bodyCapture
	respBody *bodyCapture
}

func (t *transaction) RequestBody(body io.ReadCloser) io.ReadCloser {
	if body == nil {
		return nil
	}
	return &captureReadCloser{ReadCloser: body, capture: t.reqBody}
}

func (t *transaction) ResponseBody(body io.ReadCloser) io.ReadCloser {
	if body == nil {
		return nil
	}
	t.respBody = newBodyCapture(t.cfg.BodyLimitBytes)
	return &captureReadCloser{ReadCloser: body, capture: t.respBody}
}

func (t *transaction) Finish(resp *http.Response, txErr error) {
	t.entry.FinishedAt = time.Now()
	if t.reqBody != nil {
		t.entry.Request.Body = t.reqBody.snapshot()
	}
	if resp != nil {
		t.entry.Status = resp.StatusCode
		t.entry.Response = Message{
			Headers: cloneHeaders(resp.Header, t.cfg),
		}
	}
	if t.respBody != nil {
		t.entry.Response.Body = t.respBody.snapshot()
	}
	if txErr != nil {
		t.entry.Error = txErr.Error()
	}
	t.store.Add(t.entry)
}

type captureReadCloser struct {
	io.ReadCloser
	capture *bodyCapture
}

func (c *captureReadCloser) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	if n > 0 {
		c.capture.write(p[:n])
	}
	return n, err
}

type bodyCapture struct {
	limit     int64
	total     int64
	truncated bool
	buf       bytes.Buffer
}

func newBodyCapture(limit int64) *bodyCapture {
	if limit < 0 {
		limit = 0
	}
	return &bodyCapture{limit: limit}
}

func (b *bodyCapture) write(p []byte) {
	b.total += int64(len(p))
	remaining := b.limit - int64(b.buf.Len())
	if remaining <= 0 {
		if len(p) > 0 {
			b.truncated = true
		}
		return
	}
	if int64(len(p)) > remaining {
		b.buf.Write(p[:remaining])
		b.truncated = true
		return
	}
	b.buf.Write(p)
}

func (b *bodyCapture) snapshot() Body {
	if b == nil {
		return Body{}
	}
	return Body{
		Size:           b.total,
		Preview:        b.buf.String(),
		PreviewBytes:   int64(b.buf.Len()),
		Truncated:      b.truncated || b.total > int64(b.buf.Len()),
		TruncatedAfter: b.limit,
	}
}

func fillConfig(cfg config.DeveloperConfig) config.DeveloperConfig {
	def := config.DefaultDeveloperConfig()
	if cfg.CaptureLimit == 0 {
		cfg.CaptureLimit = def.CaptureLimit
	}
	if cfg.BodyLimitBytes == 0 {
		cfg.BodyLimitBytes = def.BodyLimitBytes
	}
	if cfg.HeaderValueLimitBytes == 0 {
		cfg.HeaderValueLimitBytes = def.HeaderValueLimitBytes
	}
	if len(cfg.RedactHeaders) == 0 {
		cfg.RedactHeaders = append([]string(nil), def.RedactHeaders...)
	}
	if len(cfg.RedactQueryParams) == 0 {
		cfg.RedactQueryParams = append([]string(nil), def.RedactQueryParams...)
	}
	return cfg
}

func cloneHeaders(src http.Header, cfg config.DeveloperConfig) []Header {
	redact := make(map[string]struct{}, len(cfg.RedactHeaders))
	for _, name := range cfg.RedactHeaders {
		redact[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	names := make([]string, 0, len(src))
	for name := range src {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Header, 0, len(src))
	for _, name := range names {
		values := src.Values(name)
		if _, ok := redact[strings.ToLower(name)]; ok {
			out = append(out, Header{Name: name, Value: redactedValue, Redacted: true})
			continue
		}
		for _, value := range values {
			truncated := false
			if cfg.HeaderValueLimitBytes >= 0 && len(value) > cfg.HeaderValueLimitBytes {
				value = value[:cfg.HeaderValueLimitBytes]
				truncated = true
			}
			out = append(out, Header{Name: name, Value: value, Truncated: truncated})
		}
	}
	return out
}

func redactCapturedURL(raw string, cfg config.DeveloperConfig) string {
	if raw == "" || len(cfg.RedactQueryParams) == 0 {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	values := parsed.Query()
	if len(values) == 0 {
		return raw
	}
	redact := make(map[string]struct{}, len(cfg.RedactQueryParams))
	for _, name := range cfg.RedactQueryParams {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			redact[name] = struct{}{}
		}
	}
	changed := false
	for key, vals := range values {
		if _, ok := redact[strings.ToLower(key)]; !ok {
			continue
		}
		for i := range vals {
			vals[i] = redactedValue
		}
		values[key] = vals
		changed = true
	}
	if !changed {
		return raw
	}
	parsed.RawQuery = values.Encode()
	return parsed.String()
}

func requestURL(meta listener.HTTPCaptureMeta, req *http.Request) string {
	if req.URL != nil && req.URL.IsAbs() {
		return req.URL.String()
	}
	scheme := requestScheme(meta, req)
	host := requestHost(meta, req)
	path := "/"
	if req.URL != nil {
		path = req.URL.RequestURI()
		if path == "" {
			path = "/"
		}
	}
	if host == "" {
		return path
	}
	return scheme + "://" + host + path
}

func requestScheme(meta listener.HTTPCaptureMeta, req *http.Request) string {
	if meta.Scheme != "" {
		return meta.Scheme
	}
	if req.URL != nil && req.URL.Scheme != "" {
		return req.URL.Scheme
	}
	if req.TLS != nil {
		return "https"
	}
	return "http"
}

func requestHost(meta listener.HTTPCaptureMeta, req *http.Request) string {
	if req.Host != "" {
		return req.Host
	}
	if req.URL != nil && req.URL.Host != "" {
		return req.URL.Host
	}
	host, _, err := net.SplitHostPort(meta.Target)
	if err == nil {
		return host
	}
	return meta.Target
}

func (m *Manager) certForHost(host string) (tls.Certificate, error) {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "" {
		return tls.Certificate{}, fmt.Errorf("developer: empty TLS host")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.cfg.Enabled || !m.cfg.MITMEnabled || m.ca == nil {
		return tls.Certificate{}, fmt.Errorf("developer: MITM disabled")
	}
	if cert, ok := m.certs[host]; ok {
		return cert, nil
	}
	cert, err := m.ca.leaf(host)
	if err != nil {
		return tls.Certificate{}, err
	}
	m.certs[host] = cert
	return cert, nil
}

type caMaterial struct {
	cert     *x509.Certificate
	key      *ecdsa.PrivateKey
	certPEM  []byte
	certPath string
}

func loadOrCreateCA(cfg config.DeveloperConfig) (*caMaterial, error) {
	certPath, keyPath, err := caPaths(cfg)
	if err != nil {
		return nil, err
	}
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		ca, err := parseCA(certPEM, keyPEM)
		if err == nil {
			ca.certPath = certPath
			ca.certPEM = certPEM
			return ca, nil
		}
	}
	ca, certPEM, keyPEM, err := generateCA()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return nil, fmt.Errorf("create developer CA dir: %w", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write developer CA cert: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write developer CA key: %w", err)
	}
	ca.certPath = certPath
	ca.certPEM = certPEM
	return ca, nil
}

func caPaths(cfg config.DeveloperConfig) (string, string, error) {
	certPath := strings.TrimSpace(cfg.CACertPath)
	keyPath := strings.TrimSpace(cfg.CAKeyPath)
	if certPath == "" || keyPath == "" {
		dir, err := os.UserConfigDir()
		if err != nil || dir == "" {
			dir = os.TempDir()
		}
		base := filepath.Join(dir, "clambhook", "developer")
		if certPath == "" {
			certPath = filepath.Join(base, "clambhook-developer-ca.pem")
		}
		if keyPath == "" {
			keyPath = filepath.Join(base, "clambhook-developer-ca-key.pem")
		}
	}
	return certPath, keyPath, nil
}

func generateCA() (*caMaterial, []byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Clambhook Developer Mode CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return &caMaterial{cert: cert, key: key, certPEM: certPEM}, certPEM, keyPEM, nil
}

func parseCA(certPEM, keyPEM []byte) (*caMaterial, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("developer: invalid CA certificate PEM")
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("developer: invalid CA key PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, err
	}
	return &caMaterial{cert: cert, key: key, certPEM: certPEM}, nil
}

func (c *caMaterial) leaf(host string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(48 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
		tmpl.DNSNames = nil
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return tls.X509KeyPair(certPEM, keyPEM)
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}
