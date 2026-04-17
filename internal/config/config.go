package config

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the top-level configuration.
type Config struct {
	Active   string    `toml:"active"`
	Profiles []Profile `toml:"profile"`
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
