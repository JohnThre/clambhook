package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// ProtocolControlD is the DNS upstream protocol identifier for a Control D
// (controld.com) resolver. It is a shorthand: the user supplies their own
// resolver id and clambhook expands it into the equivalent DoH/DoT/DoQ
// endpoint, TLS server name, and anycast bootstrap IPs.
const ProtocolControlD = "controld"

// Control D anycast front ends for secure DNS (DoH/DoT/DoQ). "dns.controld.com"
// serves account resolvers; "freedns.controld.com" serves the free presets.
const (
	controldCustomHost = "dns.controld.com"
	controldFreeHost   = "freedns.controld.com"
)

// Control D primary secure-DNS endpoint IPs, used as bootstrap addresses so the
// resolver hostname resolves without a chicken-and-egg DNS lookup through the
// proxy itself. Source: https://docs.controld.com/docs/control-d-ip-ranges
var (
	controldCustomBootstrap = []string{"76.76.2.22", "76.76.10.22", "2606:1a40::22", "2606:1a40:1::22"}
	controldFreeBootstrap   = []string{"76.76.2.11", "76.76.10.11", "2606:1a40::11", "2606:1a40:1::11"}
)

// ExpandControlD rewrites a protocol="controld" upstream into the equivalent
// concrete DoH/DoT/DoQ upstream. It is the single source of truth for how a
// user's own Control D resolver maps onto clambhook's encrypted DNS transports,
// so validation, the proxy, and API previews all agree.
//
// The user-supplied Name, ServerName, and BootstrapIPs override the derived
// defaults when set.
func (u DNSUpstreamConfig) ExpandControlD() (DNSUpstreamConfig, error) {
	resolver := strings.TrimSpace(u.Resolver)
	if resolver == "" {
		return DNSUpstreamConfig{}, errors.New("controld resolver is required")
	}
	if strings.ContainsAny(resolver, " \t\r\n/") {
		return DNSUpstreamConfig{}, fmt.Errorf("controld resolver %q must not contain whitespace or '/'", resolver)
	}
	host, bootstrap := controldCustomHost, controldCustomBootstrap
	if u.Free {
		host, bootstrap = controldFreeHost, controldFreeBootstrap
	}
	transport := strings.ToLower(strings.TrimSpace(u.Transport))
	if transport == "" {
		transport = "doh"
	}
	out := DNSUpstreamConfig{
		Name:         u.Name,
		Protocol:     transport,
		ServerName:   strings.TrimSpace(u.ServerName),
		BootstrapIPs: u.BootstrapIPs,
	}
	if out.Name == "" {
		out.Name = "controld:" + resolver
	}
	switch transport {
	case "doh":
		out.URL = "https://" + host + "/" + resolver
		if out.ServerName == "" {
			out.ServerName = host
		}
	case "dot", "doq":
		fqdn := resolver + "." + host
		out.Address = net.JoinHostPort(fqdn, "853")
		if out.ServerName == "" {
			out.ServerName = fqdn
		}
	default:
		return DNSUpstreamConfig{}, fmt.Errorf("controld transport %q must be doh, dot, or doq", u.Transport)
	}
	if len(out.BootstrapIPs) == 0 {
		out.BootstrapIPs = append([]string(nil), bootstrap...)
	}
	return out, nil
}
