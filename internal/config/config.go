package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the top-level configuration.
type Config struct {
	Path      string          `toml:"-" json:"-"`
	Active    string          `toml:"active"`
	Profiles  []Profile       `toml:"profile"`
	Geo       GeoConfig       `toml:"geo"`
	Traffic   TrafficConfig   `toml:"traffic"`
	Developer DeveloperConfig `toml:"developer"`
}

// GeoConfig points at an MMDB file for IP → country/city lookups. Geo is a
// display-side feature and applies across profiles, so it lives at the top
// level rather than per-profile.
type GeoConfig struct {
	// Database is the path to an MMDB file (MaxMind GeoLite2 or a
	// schema-compatible vendor). Relative paths are resolved against the
	// config file's directory by Load. Empty = geo disabled.
	Database string `toml:"database"`
}

// TrafficConfig controls the local traffic-detail recorder. The recorder is
// metadata-only: it stores connection targets, timings, byte counts, and hop
// state, never payload bytes.
type TrafficConfig struct {
	Enabled       bool     `toml:"enabled"`
	HistoryLimit  int      `toml:"history_limit"`
	HistoryMaxAge Duration `toml:"history_max_age"`
	HistoryPath   string   `toml:"history_path"`
}

// DefaultTrafficConfig returns conservative defaults for end-user traffic
// details. Persistence is enabled but capped and local-only.
func DefaultTrafficConfig() TrafficConfig {
	return TrafficConfig{
		Enabled:       true,
		HistoryLimit:  500,
		HistoryMaxAge: Duration(168 * time.Hour),
	}
}

// DeveloperConfig controls the opt-in HTTP(S) debugging inspector. It is
// separate from TrafficConfig because it can retain headers and bounded body
// previews, whereas normal traffic history is metadata-only.
type DeveloperConfig struct {
	Enabled               bool                            `toml:"enabled" json:"enabled"`
	MITMEnabled           bool                            `toml:"mitm_enabled" json:"mitm_enabled"`
	CaptureLimit          int                             `toml:"capture_limit" json:"capture_limit"`
	BodyLimitBytes        int64                           `toml:"body_limit_bytes" json:"body_limit_bytes"`
	HeaderValueLimitBytes int                             `toml:"header_value_limit_bytes" json:"header_value_limit_bytes"`
	RedactHeaders         []string                        `toml:"redact_headers" json:"redact_headers"`
	RedactQueryParams     []string                        `toml:"redact_query_params" json:"redact_query_params"`
	CACertPath            string                          `toml:"ca_cert_path" json:"ca_cert_path,omitempty"`
	CAKeyPath             string                          `toml:"ca_key_path" json:"ca_key_path,omitempty"`
	MapRules              []DeveloperMapRuleConfig        `toml:"map_rule" json:"map_rules,omitempty"`
	BreakpointRules       []DeveloperBreakpointRuleConfig `toml:"breakpoint_rule" json:"breakpoint_rules,omitempty"`
}

// DeveloperMatchConfig describes a simple HTTP capture/tooling matcher.
// Empty fields match all requests.
type DeveloperMatchConfig struct {
	Methods     []string `toml:"methods" json:"methods,omitempty"`
	Host        string   `toml:"host" json:"host,omitempty"`
	PathPrefix  string   `toml:"path_prefix" json:"path_prefix,omitempty"`
	URLContains string   `toml:"url_contains" json:"url_contains,omitempty"`
}

// DeveloperMapRuleConfig rewrites matching HTTP(S) developer traffic.
type DeveloperMapRuleConfig struct {
	ID        string               `toml:"id" json:"id"`
	Name      string               `toml:"name" json:"name,omitempty"`
	Enabled   bool                 `toml:"enabled" json:"enabled"`
	Match     DeveloperMatchConfig `toml:"match" json:"match"`
	Kind      string               `toml:"kind" json:"kind"`
	LocalPath string               `toml:"local_path" json:"local_path,omitempty"`
	RemoteURL string               `toml:"remote_url" json:"remote_url,omitempty"`
	Status    int                  `toml:"status" json:"status,omitempty"`
	Headers   map[string]string    `toml:"headers" json:"headers,omitempty"`
}

// DeveloperBreakpointRuleConfig pauses matching developer traffic.
type DeveloperBreakpointRuleConfig struct {
	ID      string               `toml:"id" json:"id"`
	Name    string               `toml:"name" json:"name,omitempty"`
	Enabled bool                 `toml:"enabled" json:"enabled"`
	Match   DeveloperMatchConfig `toml:"match" json:"match"`
	Stage   string               `toml:"stage" json:"stage"`
}

// DefaultDeveloperConfig keeps developer mode disabled while defining bounded
// capture defaults for users who explicitly enable it.
func DefaultDeveloperConfig() DeveloperConfig {
	return DeveloperConfig{
		Enabled:               false,
		MITMEnabled:           false,
		CaptureLimit:          200,
		BodyLimitBytes:        64 << 10,
		HeaderValueLimitBytes: 8 << 10,
		RedactHeaders: []string{
			"authorization",
			"proxy-authorization",
			"cookie",
			"set-cookie",
			"x-api-key",
			"api-key",
			"x-auth-token",
			"x-csrf-token",
			"x-xsrf-token",
			"csrf-token",
			"xsrf-token",
		},
		RedactQueryParams: []string{
			"token",
			"access_token",
			"refresh_token",
			"id_token",
			"api_key",
			"apikey",
			"key",
			"secret",
			"password",
			"passwd",
			"code",
			"session",
			"auth",
		},
	}
}

// Profile represents a named configuration profile.
type Profile struct {
	Name              string                   `toml:"name"`
	Listen            ListenConfig             `toml:"listen"`
	API               APIConfig                `toml:"api"`
	DNS               DNSConfig                `toml:"dns"`
	Chains            []ChainConfig            `toml:"chain"`
	PolicyGroups      []PolicyGroupConfig      `toml:"policy_group"`
	RuleSets          []RuleSetConfig          `toml:"rule_set"`
	Rules             []RuleConfig             `toml:"rule"`
	RuleSubscriptions []RuleSubscriptionConfig `toml:"rule_subscription"`
}

// DNSConfig controls the profile-local encrypted DNS proxy used by TUN mode.
// When enabled, DNS traffic from the packet tunnel is answered locally and
// forwarded to the configured encrypted upstreams.
type DNSConfig struct {
	Enabled   bool                `toml:"enabled" json:"enabled"`
	Timeout   Duration            `toml:"timeout" json:"timeout,omitempty"`
	Upstreams []DNSUpstreamConfig `toml:"upstream" json:"upstreams,omitempty"`
}

// DNSUpstreamConfig defines one encrypted DNS resolver endpoint.
type DNSUpstreamConfig struct {
	Name         string   `toml:"name" json:"name,omitempty"`
	Protocol     string   `toml:"protocol" json:"protocol"`
	URL          string   `toml:"url" json:"url,omitempty"`
	Address      string   `toml:"address" json:"address,omitempty"`
	ServerName   string   `toml:"server_name" json:"server_name,omitempty"`
	BootstrapIPs []string `toml:"bootstrap_ips" json:"bootstrap_ips,omitempty"`
}

// ChainConfig defines a proxy chain.
type ChainConfig struct {
	Name    string         `toml:"name"`
	Servers []ServerConfig `toml:"server"`
}

// PolicyGroupConfig defines a routing policy group.
type PolicyGroupConfig struct {
	Name     string   `toml:"name" json:"name"`
	Type     string   `toml:"type" json:"type"`
	Chains   []string `toml:"chains" json:"chains"`
	Selected string   `toml:"selected" json:"selected,omitempty"`
	Hidden   bool     `toml:"hidden" json:"hidden,omitempty"`
	TestURL  string   `toml:"test_url" json:"test_url,omitempty"`
	Interval Duration `toml:"interval" json:"interval,omitempty"`
	Timeout  Duration `toml:"timeout" json:"timeout,omitempty"`
}

// ServerConfig defines a remote server endpoint.
type ServerConfig struct {
	Name     string         `toml:"name"`
	Address  string         `toml:"address"`
	Protocol string         `toml:"protocol"`
	Settings map[string]any `toml:"settings"`
}

// RuleConfig defines one ordered traffic-routing rule. Empty matcher lists
// are wildcards. Action is one of direct, block, reject, or chain:<name>.
type RuleConfig struct {
	Name           string   `toml:"name" json:"name"`
	Action         string   `toml:"action" json:"action"`
	RuleSets       []string `toml:"rule_sets" json:"rule_sets,omitempty"`
	Domains        []string `toml:"domains" json:"domains,omitempty"`
	DomainSuffixes []string `toml:"domain_suffixes" json:"domain_suffixes,omitempty"`
	DomainKeywords []string `toml:"domain_keywords" json:"domain_keywords,omitempty"`
	CIDRs          []string `toml:"cidrs" json:"cidrs,omitempty"`
	SourceCIDRs    []string `toml:"source_cidrs" json:"source_cidrs,omitempty"`
	Ports          []int    `toml:"ports" json:"ports,omitempty"`
	Networks       []string `toml:"networks" json:"networks,omitempty"`
}

// RuleSetConfig defines a named reusable matcher set. Inline entries are
// always available; remote entries are cached after an explicit refresh.
type RuleSetConfig struct {
	Name           string   `toml:"name" json:"name"`
	Domains        []string `toml:"domains" json:"domains,omitempty"`
	DomainSuffixes []string `toml:"domain_suffixes" json:"domain_suffixes,omitempty"`
	DomainKeywords []string `toml:"domain_keywords" json:"domain_keywords,omitempty"`
	CIDRs          []string `toml:"cidrs" json:"cidrs,omitempty"`
	URL            string   `toml:"url" json:"url,omitempty"`
	Format         string   `toml:"format" json:"format,omitempty"`
	Disabled       bool     `toml:"disabled" json:"disabled,omitempty"`
}

// RuleSubscriptionConfig defines one cached blocklist subscription. The
// subscription expands to generated block/reject rules at runtime.
type RuleSubscriptionConfig struct {
	Name     string   `toml:"name" json:"name"`
	URL      string   `toml:"url" json:"url"`
	Format   string   `toml:"format" json:"format,omitempty"`
	Action   string   `toml:"action" json:"action,omitempty"`
	Networks []string `toml:"networks" json:"networks,omitempty"`
	Disabled bool     `toml:"disabled" json:"disabled,omitempty"`
}

// ListenConfig defines local proxy listener addresses.
type ListenConfig struct {
	SOCKS5                 string      `toml:"socks5"`
	SOCKS5Chain            string      `toml:"socks5_chain"`
	SOCKS5Auth             *SOCKS5Auth `toml:"socks5_auth,omitempty"`
	SOCKS5MaxConns         int         `toml:"socks5_max_connections"`
	SOCKS5HandshakeTimeout Duration    `toml:"socks5_handshake_timeout"`
	HTTP                   string      `toml:"http"`
	HTTPChain              string      `toml:"http_chain"`
	HTTPAuth               *HTTPAuth   `toml:"http_auth,omitempty"`
	HTTPMaxConns           int         `toml:"http_max_connections"`
	HTTPHandshakeTimeout   Duration    `toml:"http_handshake_timeout"`
	TUN                    *TUNConfig  `toml:"tun,omitempty"`
}

// TUNConfig defines device-wide packet routing. It is opt-in because the
// Linux daemon listener changes host routing and requires root or CAP_NET_ADMIN.
type TUNConfig struct {
	Enabled      bool     `toml:"enabled"`
	Name         string   `toml:"name"`
	Chain        string   `toml:"chain"`
	MTU          int      `toml:"mtu"`
	Addresses    []string `toml:"addresses"`
	Routes       []string `toml:"routes"`
	ExcludeCIDRs []string `toml:"exclude_cidrs"`
}

// Duration is a time.Duration that parses from a TOML string like "30s" or
// "2m". BurntSushi/toml supports this via a TextUnmarshaler.
type Duration time.Duration

// UnmarshalText parses a Go-duration-formatted string.
func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// MarshalText writes durations back in Go-duration form for generated TOML.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

// Std returns the value as a standard library time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// SOCKS5Auth carries optional RFC 1929 credentials for the SOCKS5 listener.
// Presence of this stanza (even with empty fields) switches the listener to
// require username/password authentication.
type SOCKS5Auth struct {
	Username string `toml:"username"`
	Password string `toml:"password"`
}

// HTTPAuth carries optional RFC 7617 Basic credentials for the HTTP proxy
// listener. Presence of this stanza switches the listener to require a
// valid Proxy-Authorization: Basic <base64> header.
type HTTPAuth struct {
	Username string `toml:"username"`
	Password string `toml:"password"`
}

// APIConfig defines the API server settings.
type APIConfig struct {
	Listen string `toml:"listen"`
}

// Load reads and parses a TOML config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := Config{
		Traffic:   DefaultTrafficConfig(),
		Developer: DefaultDeveloperConfig(),
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	cfg.Path = path

	// Resolve a relative geo.database path against the config file's
	// directory — matches how users intuitively think about TOML paths.
	if cfg.Geo.Database != "" && !filepath.IsAbs(cfg.Geo.Database) {
		cfg.Geo.Database = filepath.Join(filepath.Dir(path), cfg.Geo.Database)
	}
	if cfg.Traffic.HistoryPath != "" && !filepath.IsAbs(cfg.Traffic.HistoryPath) {
		cfg.Traffic.HistoryPath = filepath.Join(filepath.Dir(path), cfg.Traffic.HistoryPath)
	}
	if cfg.Developer.CACertPath != "" && !filepath.IsAbs(cfg.Developer.CACertPath) {
		cfg.Developer.CACertPath = filepath.Join(filepath.Dir(path), cfg.Developer.CACertPath)
	}
	if cfg.Developer.CAKeyPath != "" && !filepath.IsAbs(cfg.Developer.CAKeyPath) {
		cfg.Developer.CAKeyPath = filepath.Join(filepath.Dir(path), cfg.Developer.CAKeyPath)
	}

	return &cfg, nil
}

// ActiveProfile returns the currently active profile.
func (c *Config) ActiveProfile() (*Profile, error) {
	for i := range c.Profiles {
		if c.Profiles[i].Name == c.Active {
			return &c.Profiles[i], nil
		}
	}
	if len(c.Profiles) > 0 {
		return &c.Profiles[0], nil
	}
	return nil, fmt.Errorf("no profiles configured")
}
