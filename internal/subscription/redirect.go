package subscription

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// ipResolver matches the net.Resolver method used for public-host checks.
type ipResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// resolver is the host resolver used by ValidatePublicHTTPURL and redirect
// validation. Tests override it with deterministic stubs.
var resolver ipResolver = net.DefaultResolver

// ClientWithSafeRedirects returns a shallow copy of client that revalidates
// every redirect without mutating the caller's client. Same-origin redirects
// are allowed. A cross-origin redirect requires an existing CheckRedirect
// policy to explicitly allow it, and its destination must resolve entirely to
// public addresses.
func ClientWithSafeRedirects(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	redirectClient := *client
	configuredPolicy := client.CheckRedirect
	redirectClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) == 0 {
			return errors.New("redirect has no originating request")
		}
		if len(via) >= 10 && configuredPolicy == nil {
			return errors.New("stopped after 10 redirects")
		}
		if err := validateRedirectURL(req.URL); err != nil {
			return err
		}
		if sameOrigin(req.URL, via[0].URL) {
			if configuredPolicy != nil {
				return configuredPolicy(req, via)
			}
			return nil
		}
		if err := validatePublicRedirectHost(req.Context(), req.URL.Hostname()); err != nil {
			return err
		}
		if configuredPolicy == nil {
			return fmt.Errorf("redirect to different origin %q is not allowed", req.URL.Host)
		}
		return configuredPolicy(req, via)
	}
	return &redirectClient
}

// ValidatePublicHTTPURL applies the same public-host SSRF policy used for
// redirects to an initial request URL. It rejects non-HTTP(S) URLs, missing
// hosts, and any host that is loopback, private, link-local, unspecified,
// multicast, CGNAT, or resolves entirely to such addresses.
func ValidatePublicHTTPURL(ctx context.Context, u *url.URL) error {
	if err := validateRedirectURL(u); err != nil {
		return err
	}
	if err := validatePublicRedirectHost(ctx, u.Hostname()); err != nil {
		return err
	}
	return nil
}

func validateRedirectURL(target *url.URL) error {
	if target == nil {
		return errors.New("redirect target URL is missing")
	}
	scheme := strings.ToLower(target.Scheme)
	if (scheme != "http" && scheme != "https") || target.Host == "" || target.Hostname() == "" {
		return fmt.Errorf("redirect target %q must be an http or https URL with a host", target.String())
	}
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil || !strings.EqualFold(a.Scheme, b.Scheme) {
		return false
	}
	if !strings.EqualFold(normalizeRedirectHost(a.Hostname()), normalizeRedirectHost(b.Hostname())) {
		return false
	}
	return effectivePort(a) == effectivePort(b)
}

func normalizeRedirectHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func validatePublicRedirectHost(ctx context.Context, rawHost string) error {
	host := normalizeRedirectHost(rawHost)
	if host == "" {
		return errors.New("redirect target host is empty")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || isMetadataHost(host) {
		return fmt.Errorf("redirect target host %q is not public", rawHost)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if unsafeRedirectAddr(addr) {
			return fmt.Errorf("redirect target address %q is not public", addr)
		}
		return nil
	}

	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve redirect target host %q: %w", rawHost, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("redirect target host %q resolved to no addresses", rawHost)
	}
	for _, addr := range addrs {
		if unsafeRedirectAddr(addr) {
			return fmt.Errorf("redirect target host %q resolves to non-public address %q", rawHost, addr)
		}
	}
	return nil
}

func isMetadataHost(host string) bool {
	switch host {
	case "metadata", "instance-data", "metadata.google.internal", "metadata.azure.internal":
		return true
	default:
		return false
	}
}

func unsafeRedirectAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() {
		return true
	}
	// Carrier-grade NAT is not publicly routable. 100.100.100.200 is also
	// used by cloud instance-metadata services, as is 192.0.0.192.
	return netip.MustParsePrefix("100.64.0.0/10").Contains(addr) ||
		addr == netip.MustParseAddr("192.0.0.192")
}
