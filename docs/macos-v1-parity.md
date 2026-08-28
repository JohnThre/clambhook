<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

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
- have: MitM/SSL decrypt via opt-in HTTP Capture, with a per-host SSL decrypt
  allowlist (wildcard hostname patterns) to restrict which CONNECT hosts get
  decrypted.
- out of scope: scripting engine. Intentionally not planned for v1 or v1.1;
  rule-based routing plus the daemon API cover the targeted workflows.

### Little Snitch

- have: domain and country hierarchy, allow/block/reject plus temporary rules,
  rule usage stats, cleanup suggestions, and per-network profile switching.
- have: per-process attribution and interactive connection prompts for
  local-proxy traffic (SOCKS5/HTTP listeners) on darwin and linux. The daemon
  maps a connection's source socket to the owning process, matches rules on a
  `processes` matcher, and pauses undecided connections for an allow/block
  choice (`prompt` config, `GET/POST /api/v1/prompts/*`, surfaced in the TUI).
  The additive C17 listener now has native Darwin/Linux socket attribution and
  process-rule matching parity; prompt persistence and control-API parity are
  still required before the Go rollback can be removed.
- partial: activity filtering, quick filters, and free-text/token search.
- deferred: interactive prompts inside the Apple GUI via a system-wide
  content-filter Network Extension (all-app attribution, not just proxied
  traffic) would require Apple's Network Extension approval and remain out of
  this release; the daemon-side prompts above cover proxied traffic without it.

### Proxyman

- have: HTTP(S) capture list, request/response detail, breakpoints, map local,
  map remote, repeat, HAR export, and CA install/trust.
- have: compose / edit-and-send request through the daemon. Compose uses the
  standalone `/api/v1/developer/send` endpoint; repeat re-sends a captured
  transaction through `/api/v1/developer/repeat`.
- have: cURL import and export. A captured transaction serializes to a runnable
  cURL command (`GET /api/v1/developer/entries/{id}/curl`), and a pasted cURL
  command parses back into the composer (`POST /api/v1/developer/curl/import`).
  The importer is a bounded, stdlib-only shell tokenizer; it accepts the common
  cURL flag subset and best-effort ignores unknown flags.
- have: server-side flow-list filtering (method, status range, host, scheme,
  content type, errors-only, and free-text search over method/URL/host/chain/
  status/error/headers/body previews) via `GET /api/v1/developer/entries` query
  params, so all clients share one filter semantics.
- have: daemon-side body viewers (pretty JSON/XML/form/HTML plus a hex dump)
  computed once and rendered by every client, with a local JSON re-indent
  fallback for older daemons.
- have: HAR 1.2 timings (connect/SSL/send/wait/receive) captured around network
  I/O only, so breakpoint and rewrite pauses are excluded from the breakdown.
- have (v1.1): network throttling / conditioner (per-profile bandwidth caps,
  latency/jitter, packet-loss, live-toggled via `GET`/`PUT /api/v1/conditioner`)
  and daemon-side protocol-specific viewers (WebSocket / gRPC / GraphQL) rendered
  across all four clients. See docs/roadmap.md.
- out of scope: Proxyman-style save sessions / automatic request grouping rules
  are not planned; rule-based routing plus the capture filter cover the targeted
  workflows.

## Release-Gating Decision

Enhanced Mode is the macOS device-wide routing path for v1. Any Apple Network
Extension or content-filter feature should remain gray/disabled in product
planning until Apple grants the required capability and a separate signed
hardware validation plan exists.
