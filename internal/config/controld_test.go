package config

import (
	"strings"
	"testing"
)

func TestExpandControlDCustomDefaultsToDoH(t *testing.T) {
	up := DNSUpstreamConfig{Protocol: "controld", Resolver: "abc123"}
	got, err := up.ExpandControlD()
	if err != nil {
		t.Fatalf("ExpandControlD: %v", err)
	}
	if got.Protocol != "doh" {
		t.Fatalf("protocol = %q, want doh", got.Protocol)
	}
	if got.URL != "https://dns.controld.com/abc123" {
		t.Fatalf("url = %q", got.URL)
	}
	if got.ServerName != "dns.controld.com" {
		t.Fatalf("server_name = %q", got.ServerName)
	}
	if got.Name != "controld:abc123" {
		t.Fatalf("name = %q", got.Name)
	}
	if len(got.BootstrapIPs) == 0 || got.BootstrapIPs[0] != "76.76.2.22" {
		t.Fatalf("bootstrap = %#v, want custom anycast IPs", got.BootstrapIPs)
	}
}

func TestExpandControlDDoTAndDoQUseFQDN(t *testing.T) {
	for _, transport := range []string{"dot", "doq"} {
		up := DNSUpstreamConfig{Protocol: "controld", Resolver: "abc123", Transport: transport}
		got, err := up.ExpandControlD()
		if err != nil {
			t.Fatalf("%s: ExpandControlD: %v", transport, err)
		}
		if got.Protocol != transport {
			t.Fatalf("%s: protocol = %q", transport, got.Protocol)
		}
		if got.Address != "abc123.dns.controld.com:853" {
			t.Fatalf("%s: address = %q", transport, got.Address)
		}
		if got.ServerName != "abc123.dns.controld.com" {
			t.Fatalf("%s: server_name = %q", transport, got.ServerName)
		}
		if got.URL != "" {
			t.Fatalf("%s: url = %q, want empty", transport, got.URL)
		}
	}
}

func TestExpandControlDFreePreset(t *testing.T) {
	up := DNSUpstreamConfig{Protocol: "controld", Free: true, Resolver: "p2"}
	got, err := up.ExpandControlD()
	if err != nil {
		t.Fatalf("ExpandControlD: %v", err)
	}
	if got.URL != "https://freedns.controld.com/p2" {
		t.Fatalf("url = %q", got.URL)
	}
	if got.ServerName != "freedns.controld.com" {
		t.Fatalf("server_name = %q", got.ServerName)
	}
	if len(got.BootstrapIPs) == 0 || got.BootstrapIPs[0] != "76.76.2.11" {
		t.Fatalf("bootstrap = %#v, want free anycast IPs", got.BootstrapIPs)
	}
}

func TestExpandControlDHonorsOverrides(t *testing.T) {
	up := DNSUpstreamConfig{
		Protocol:     "controld",
		Resolver:     "abc123",
		Name:         "custom",
		ServerName:   "override.example",
		BootstrapIPs: []string{"9.9.9.9"},
	}
	got, err := up.ExpandControlD()
	if err != nil {
		t.Fatalf("ExpandControlD: %v", err)
	}
	if got.Name != "custom" {
		t.Fatalf("name = %q, want custom override", got.Name)
	}
	if got.ServerName != "override.example" {
		t.Fatalf("server_name = %q, want override", got.ServerName)
	}
	if len(got.BootstrapIPs) != 1 || got.BootstrapIPs[0] != "9.9.9.9" {
		t.Fatalf("bootstrap = %#v, want override", got.BootstrapIPs)
	}
}

func TestExpandControlDErrors(t *testing.T) {
	tests := []struct {
		name string
		up   DNSUpstreamConfig
		want string
	}{
		{"missing resolver", DNSUpstreamConfig{Protocol: "controld"}, "resolver is required"},
		{"bad resolver", DNSUpstreamConfig{Protocol: "controld", Resolver: "a/b"}, "must not contain"},
		{"bad transport", DNSUpstreamConfig{Protocol: "controld", Resolver: "abc", Transport: "udp"}, "must be doh, dot, or doq"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.up.ExpandControlD()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}
