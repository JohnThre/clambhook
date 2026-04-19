package geo

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	cityDB    = "testdata/GeoIP2-City-Test.mmdb"
	countryDB = "testdata/GeoIP2-Country-Test.mmdb"
)

func TestOpen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		wantNil bool
		wantErr bool
	}{
		{name: "empty path yields nil reader", path: "", wantNil: true},
		{name: "missing file errors", path: "testdata/does-not-exist.mmdb", wantErr: true},
		{name: "valid city db", path: cityDB},
		{name: "valid country db", path: countryDB},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := Open(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNil {
				if r != nil {
					t.Fatalf("expected nil reader for empty path, got %+v", r)
				}
				return
			}
			if r == nil {
				t.Fatalf("expected non-nil reader")
			}
			defer r.Close()
		})
	}
}

func TestLookupIP_CityDB(t *testing.T) {
	t.Parallel()

	r := mustOpen(t, cityDB)
	defer r.Close()

	// 81.2.69.142 is a stable test IP in MaxMind's GeoIP2-City-Test.mmdb
	// documented to decode to London, United Kingdom.
	loc, err := r.LookupIP(net.ParseIP("81.2.69.142"))
	if err != nil {
		t.Fatalf("LookupIP: %v", err)
	}
	if loc.CountryCode != "GB" {
		t.Errorf("CountryCode = %q, want GB", loc.CountryCode)
	}
	if loc.Country != "United Kingdom" {
		t.Errorf("Country = %q, want United Kingdom", loc.Country)
	}
	if loc.City != "London" {
		t.Errorf("City = %q, want London", loc.City)
	}
	if loc.Latitude == 0 || loc.Longitude == 0 {
		t.Errorf("expected non-zero lat/lon, got %v, %v", loc.Latitude, loc.Longitude)
	}
}

func TestLookupIP_CountryDBLeavesCityEmpty(t *testing.T) {
	t.Parallel()

	r := mustOpen(t, countryDB)
	defer r.Close()

	loc, err := r.LookupIP(net.ParseIP("81.2.69.142"))
	if err != nil {
		t.Fatalf("LookupIP: %v", err)
	}
	if loc.CountryCode != "GB" {
		t.Errorf("CountryCode = %q, want GB", loc.CountryCode)
	}
	if loc.City != "" {
		t.Errorf("City = %q, want empty (country-only db)", loc.City)
	}
	if loc.Latitude != 0 || loc.Longitude != 0 {
		t.Errorf("expected zero lat/lon on country-only db, got %v, %v", loc.Latitude, loc.Longitude)
	}
}

func TestLookupIP_UnknownIP(t *testing.T) {
	t.Parallel()

	r := mustOpen(t, cityDB)
	defer r.Close()

	// Reserved-documentation range; not in the test DB.
	loc, err := r.LookupIP(net.ParseIP("203.0.113.1"))
	if err != nil {
		t.Fatalf("LookupIP: %v", err)
	}
	if *loc != (Location{}) {
		t.Errorf("expected empty Location for unknown IP, got %+v", loc)
	}
}

func TestLookup_StripsPort(t *testing.T) {
	t.Parallel()

	r := mustOpen(t, cityDB)
	defer r.Close()

	loc, err := r.Lookup("81.2.69.142:443")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if loc.CountryCode != "GB" {
		t.Errorf("CountryCode = %q, want GB", loc.CountryCode)
	}
}

func TestLookup_ResolvesHostnameViaStubResolver(t *testing.T) {
	r := mustOpen(t, cityDB)
	defer r.Close()

	// Sequential — tweaks the package resolver, so can't run t.Parallel.
	orig := resolver
	resolver = fakeResolver{ip: net.ParseIP("81.2.69.142")}
	defer func() { resolver = orig }()

	loc, err := r.Lookup("some-server.example.com:443")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if loc.CountryCode != "GB" {
		t.Errorf("CountryCode = %q, want GB", loc.CountryCode)
	}
}

func TestLookup_ResolverErrorPropagates(t *testing.T) {
	r := mustOpen(t, cityDB)
	defer r.Close()

	orig := resolver
	resolver = fakeResolver{err: errors.New("no such host")}
	defer func() { resolver = orig }()

	_, err := r.Lookup("nope.example.com")
	if err == nil {
		t.Fatal("expected error from failed DNS")
	}
}

func TestLookup_NilReader(t *testing.T) {
	t.Parallel()

	var r *Reader
	loc, err := r.Lookup("81.2.69.142")
	if err != nil {
		t.Fatalf("nil reader should not error, got %v", err)
	}
	if *loc != (Location{}) {
		t.Errorf("nil reader should return empty Location, got %+v", loc)
	}
}

func TestLookup_ClosedReader(t *testing.T) {
	t.Parallel()

	r := mustOpen(t, cityDB)
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	loc, err := r.LookupIP(net.ParseIP("81.2.69.142"))
	if err != nil {
		t.Fatalf("closed reader should not error, got %v", err)
	}
	if *loc != (Location{}) {
		t.Errorf("closed reader should return empty Location, got %+v", loc)
	}
	// Second Close is a no-op.
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestReader_ConcurrentLookupDuringClose(t *testing.T) {
	// Exercises the Reader.mu ordering: many readers call LookupIP while one
	// writer calls Close. With -race this fails loudly if the RWMutex
	// discipline is broken.
	t.Parallel()

	r := mustOpen(t, cityDB)
	ip := net.ParseIP("81.2.69.142")

	var wg sync.WaitGroup
	var done atomic.Bool

	const readers = 8
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !done.Load() {
				_, _ = r.LookupIP(ip)
			}
		}()
	}

	// Let readers spin up, then close.
	for i := 0; i < 1000; i++ {
		if _, err := r.LookupIP(ip); err != nil {
			t.Fatalf("pre-close lookup: %v", err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	done.Store(true)
	wg.Wait()
}

// --- helpers ---

func mustOpen(t *testing.T, path string) *Reader {
	t.Helper()
	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	if r == nil {
		t.Fatalf("Open(%q): nil reader", path)
	}
	return r
}

type fakeResolver struct {
	ip  net.IP
	err error
}

func (f fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []net.IPAddr{{IP: f.ip}}, nil
}
