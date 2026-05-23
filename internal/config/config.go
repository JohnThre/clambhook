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
	Active   string    `toml:"active"`
	Profiles []Profile `toml:"profile"`
	Geo      GeoConfig `toml:"geo"`
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

// Profile represents a named configuration profile.
type Profile struct {
	Name   string        `toml:"name"`
	Listen ListenConfig  `toml:"listen"`
	API    APIConfig     `toml:"api"`
	Chains []ChainConfig `toml:"chain"`
}

// ChainConfig defines a proxy chain.
type ChainConfig struct {
	Name    string         `toml:"name"`
	Servers []ServerConfig `toml:"server"`
}

// ServerConfig defines a remote server endpoint.
type ServerConfig struct {
	Name     string         `toml:"name"`
	Address  string         `toml:"address"`
	Protocol string         `toml:"protocol"`
	Settings map[string]any `toml:"settings"`
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

// TUNConfig defines the Linux device-wide TUN listener. It is opt-in because
// it changes host routing and requires root or CAP_NET_ADMIN on Linux.
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

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Resolve a relative geo.database path against the config file's
	// directory — matches how users intuitively think about TOML paths.
	if cfg.Geo.Database != "" && !filepath.IsAbs(cfg.Geo.Database) {
		cfg.Geo.Database = filepath.Join(filepath.Dir(path), cfg.Geo.Database)
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
