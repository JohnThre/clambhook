# macOS v1 Scope

macOS v1 is proxy-only.

## Included

- Launch the local clambhook daemon from the macOS app.
- Expose local SOCKS5 and HTTP proxy listeners from the daemon.
- Optionally configure macOS system HTTP, HTTPS, and SOCKS proxy settings to point at those listeners.
- Show status, routing decisions, counters, and traffic history for traffic that enters the configured clambhook proxy listeners.

## Excluded

- macOS Network Extension packet tunnel.
- Full-device VPN routing.
- Route-table ownership or default-route changes.
- DNS interception for arbitrary device traffic.
- Device-wide traffic capture for apps that bypass or ignore system proxy settings.

## Post-v1 Direction

True macOS device-wide handling should be treated as a separate architecture project. It will require a macOS Network Extension packet tunnel or another privileged traffic-ingress design, plus separate entitlement, packaging, recovery, DNS, and route-management work.
