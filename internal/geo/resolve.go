package geo

import (
	"context"
	"fmt"
	"net"
	"time"
)

// resolveTimeout bounds DNS lookups done inside Lookup. Kept short because
// geo is a display feature — callers don't want an unresponsive resolver to
// stall a status request.
const resolveTimeout = 2 * time.Second

// ipResolver is the tiny subset of *net.Resolver this package uses. Keeping
// it an interface lets tests stub DNS without a real resolver.
type ipResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

var resolver ipResolver = net.DefaultResolver

// resolveAddress parses an IP, IP:port, host, or host:port and returns an
// IP to look up. Hostnames are resolved via the package-level resolver with
// a bounded context. IPv4 is preferred over IPv6 because MMDB IPv6 records
// are often sparser; fallback to the first IPv6 if no IPv4 was returned.
func resolveAddress(address string) (net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()
	return resolveAddressCtx(ctx, address)
}

// resolveAddressCtx is the context-aware form of resolveAddress.
func resolveAddressCtx(ctx context.Context, address string) (net.IP, error) {
	if address == "" {
		return nil, fmt.Errorf("empty address")
	}

	host := address
	if h, _, err := net.SplitHostPort(address); err == nil {
		host = h
	}

	if ip := net.ParseIP(host); ip != nil {
		return ip, nil
	}

	resolveCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	addrs, err := resolver.LookupIPAddr(resolveCtx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("resolve %q: no addresses", host)
	}

	for _, a := range addrs {
		if v4 := a.IP.To4(); v4 != nil {
			return v4, nil
		}
	}
	return addrs[0].IP, nil
}
