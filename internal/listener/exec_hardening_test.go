package listener

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidIfaceName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"eth", "eth0", true},
		{"utun", "utun3", true},
		{"dotted", "en0.100", true},
		{"underscore-dash", "br_lan-0", true},
		{"empty", "", false},
		{"leading dash option", "-foo", false},
		{"shell metachar", "en0; rm -rf /", false},
		{"space", "en 0", false},
		{"slash", "en0/../x", false},
		{"newline", "en0\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validIfaceName(tc.in); got != tc.want {
				t.Fatalf("validIfaceName(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestResolveCommandPathPrefersExistingAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "ip")
	if err := os.WriteFile(existing, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := resolveCommandPath("/no/such/binary/xyz", existing, "/another/missing")
	if got != existing {
		t.Fatalf("resolveCommandPath = %q, want %q", got, existing)
	}

	// When none exist, the last candidate is returned as a fallback and is
	// still an absolute path (so exec never falls back to a $PATH lookup).
	fallback := resolveCommandPath("/no/such/a", "/no/such/b")
	if fallback != "/no/such/b" {
		t.Fatalf("fallback = %q, want /no/such/b", fallback)
	}
	if !filepath.IsAbs(fallback) {
		t.Fatalf("fallback %q is not absolute", fallback)
	}
}
