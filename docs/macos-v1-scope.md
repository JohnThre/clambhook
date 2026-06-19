# macOS Scope

macOS uses Network Extension packet tunnel mode for device-wide routing. The legacy daemon proxy path remains available as a fallback for environments where a system extension is not approved or where proxy-only handling is preferred.

## Included

- Install and activate a macOS packet tunnel system extension.
- Configure a `NETunnelProviderManager` tunnel profile for the ClambHook packet tunnel provider.
- Apply tunnel network settings from the shared mobile runtime, including tunnel addresses, routes, DNS, and optional proxy settings.
- Pass packets between Network Extension packet flow and the Go tunnel runtime.
- Route test/explain requests and profile/rule edits through the provider while the tunnel is connected.
- Show status, routing decisions, counters, and traffic history from the active tunnel runtime.
- In fallback mode, launch the local clambhook daemon, expose SOCKS5 and HTTP proxy listeners, and optionally configure macOS system HTTP, HTTPS, and SOCKS proxy settings to point at those listeners.

## Limits

- System extension activation requires Apple signing/provisioning with the packet tunnel system extension entitlement and may require explicit user approval in System Settings.
- Daemon listener settings and connection-history rule creation remain daemon-mode features.
- Proxy fallback mode is not device-wide; it only handles traffic that honors macOS proxy settings.

## Packaging

The macOS app embeds `ClambhookMacTunnel.systemextension` under `Contents/Library/SystemExtensions` and includes the mobile tunnel bridge as `ClambhookMobile.xcframework`. Build the bridge with `make build-apple-mobile-xcframework` or as part of `make build-apple`.
