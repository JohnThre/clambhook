// Package geo performs IP → country/city lookups against an MMDB-formatted
// database (MaxMind GeoLite2 and schema-compatible vendors like DB-IP or
// IPinfo). It is opt-in: a nil *Reader is valid and returns empty Locations,
// so callers don't need to branch on whether geo is configured.
package geo

import (
	"context"
	"fmt"
	"net"
	"sync"
	"github.com/oschwald/maxminddb-golang"
)

// Location represents a server's geographic location. Zero values are used
// for fields the database doesn't carry (e.g. Country-only DBs leave City,
// Latitude and Longitude empty).
type Location struct {
	Country     string  `json:"country,omitempty"`
	CountryCode string  `json:"country_code,omitempty"`
	City        string  `json:"city,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
}

// Reader wraps an MMDB reader for IP → Location lookups.
//
// A nil *Reader is valid: all methods treat it as "geo disabled" and return
// an empty Location with nil error. This lets callers hold a *Reader field
// without null-checking on every lookup path.
//
// Reader is safe for concurrent use; Close serializes against in-flight
// Lookups via an internal RWMutex so hot-swap-and-close under Engine.Reload
// is race-free.
type Reader struct {
	db     *maxminddb.Reader
	mu     sync.RWMutex
	closed bool
}

// mmdbRecord is the subset of MMDB fields this package reads. Decoding into
// our own struct (rather than binding to geoip2-golang's typed shape) keeps
// us compatible with MaxMind GeoLite2, DB-IP, and IPinfo MMDBs, whose
// layouts share these top-level keys.
type mmdbRecord struct {
	Country struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"country"`
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Location struct {
		Latitude  float64 `maxminddb:"latitude"`
		Longitude float64 `maxminddb:"longitude"`
	} `maxminddb:"location"`
}

// Open opens an MMDB file. An empty path returns (nil, nil) — the intended
// way to spell "geo disabled" from a config loader. Any other error
// (missing file, bad format, wrong magic) is returned as-is.
func Open(path string) (*Reader, error) {
	if path == "" {
		return nil, nil
	}
	db, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open geo db %q: %w", path, err)
	}
	return &Reader{db: db}, nil
}

// LookupCtx is the context-aware form of Lookup. The supplied ctx bounds
// DNS resolution; a cancelled ctx returns early without blocking.
func (r *Reader) LookupCtx(ctx context.Context, address string) (*Location, error) {
	if r == nil {
		return &Location{}, nil
	}
	ip, err := resolveAddressCtx(ctx, address)
	if err != nil {
		return nil, err
	}
	return r.LookupIP(ip)
}

// Lookup resolves a host, IP, or host:port to a Location.
//
// A nil or closed Reader returns an empty Location and nil error — the
// feature degrades silently so an unconfigured or stale reader never breaks
// the caller.s happy path. IPs not present in the database likewise return
// an empty Location with nil error.
func (r *Reader) Lookup(address string) (*Location, error) {
	if r == nil {
		return &Location{}, nil
	}
	ip, err := resolveAddress(address)
	if err != nil {
		return nil, err
	}
	return r.LookupIP(ip)
}

// LookupIP is the DNS-free form of Lookup. Prefer it when the caller
// already holds an IP (e.g. a connection's RemoteAddr).
func (r *Reader) LookupIP(ip net.IP) (*Location, error) {
	if r == nil {
		return &Location{}, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return &Location{}, nil
	}

	var rec mmdbRecord
	if err := r.db.Lookup(ip, &rec); err != nil {
		return nil, fmt.Errorf("mmdb lookup %s: %w", ip, err)
	}
	return recordToLocation(&rec), nil
}

// Close releases the underlying MMDB file. It is nil-safe and idempotent;
// re-calling Close or calling Lookup afterwards returns gracefully.
func (r *Reader) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return r.db.Close()
}

// recordToLocation maps an MMDB record to our Location. English names are
// preferred; callers that need other locales can extend Location and read
// rec.Country.Names / rec.City.Names directly. Zero values are returned
// for any field the DB doesn't carry.
func recordToLocation(rec *mmdbRecord) *Location {
	return &Location{
		Country:     rec.Country.Names["en"],
		CountryCode: rec.Country.ISOCode,
		City:        rec.City.Names["en"],
		Latitude:    rec.Location.Latitude,
		Longitude:   rec.Location.Longitude,
	}
}
