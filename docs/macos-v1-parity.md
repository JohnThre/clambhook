# macOS v1 Feature Parity

UI/UX reference: Surge for macOS. Feature reference: Surge, Little Snitch,
Proxyman. Minimum OS: macOS 14.0 (Apple Silicon).

## Current Direction

ClambHook v1 uses daemon-backed routing on macOS:

- System Proxy mode for apps that honor macOS proxy settings.
- Enhanced Mode for device-wide routing through a privileged daemon-created
  utun interface.

Apple Network Extension and System Extension features are intentionally not part
of this release. This keeps the product independent from restricted Apple
capability approvals while preserving a practical direct-download macOS path.

## Feature-Parity Gap Matrix

### Surge

- have: policy-group switching, latency/benchmark tests, rule tester and
  explain, profile import/export, encrypted DNS, rule subscriptions, and
  full-tunnel versus proxy-mode clarity.
- have: Enhanced Mode-style device-wide routing on macOS through utun.
- partial: MitM/SSL decrypt exists via Developer capture, but there is no
  per-host SSL allowlist UX.
- missing: scripting engine.

### Little Snitch

- have: domain and country hierarchy, allow/block/reject plus temporary rules,
  rule usage stats, cleanup suggestions, and per-network profile switching.
- partial: activity filtering, quick filters, and free-text/token search.
- deferred: per-process attribution and interactive connection prompts. These
  would require Apple's content-filter Network Extension approval, so they are
  intentionally stopped for this release.

### Proxyman

- have: HTTP(S) capture list, request/response detail, breakpoints, map local,
  map remote, repeat, HAR export, and CA install/trust.
- have: compose / edit-and-send request through the daemon Repeat endpoint.
- missing: network throttling / conditioner and protocol-specific viewers
  (WebSocket / gRPC / GraphQL).

## Release-Gating Decision

Enhanced Mode is the macOS device-wide routing path for v1. Any Apple Network
Extension or content-filter feature should remain gray/disabled in product
planning until Apple grants the required capability and a separate signed
hardware validation plan exists.
