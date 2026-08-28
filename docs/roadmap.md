<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# ClambHook Roadmap

Committed direction beyond the current macOS v1 release. Items here are planned
work, not shipped features. See `docs/macos-v1-parity.md` for what ships in v1.

## Sequencing

The `docs/project-review.md` release and security blockers are closed, so the
post-v1 work is unblocked and sequenced as:

1. **C runtime and native Linux migration. In progress.** Replace the Go
   daemon/TUI/mobile backend with C17, replace the GNU/Linux Compose client with
   C/GTK 4, and retain Kotlin/Jetpack Compose on Android over JNI. Contract,
   differential, packaging, and API 31/33/36 gates are defined in
   [`c-migration.md`](c-migration.md); the legacy implementations remain the
   rollback path until those gates pass.
2. **v1.1 — capture and network tooling. Shipped.** The network throttling /
   conditioner and the protocol-specific viewers are delivered end-to-end across
   the daemon and all four clients. Both built on the already-shipping daemon
   chain and HTTP capture pipeline and touched only additive API surface.
3. **Protocol expansion — Reality.** VMess and ShadowTLS transports
   already ship (`internal/protocol/vmess`, `internal/protocol/shadowtls`, both
   registered in the protocol registry). Reality is the remaining planned
   transport; now that v1.1 has shipped and capture tooling is stable against the
   current transport set, this is the next milestone.
4. **Scripting engine.** A future release candidate only, gated behind the
   parity-doc deferral below.

Each step starts only after the previous one ships, keeping one cross-client
change in flight at a time.

## v1.1 — shipped

### Network throttling / conditioner — shipped

Simulates constrained networks on the daemon chain path: bandwidth caps, added
latency/jitter, and packet-loss simulation, toggled per active profile. The
`internal/conditioner` shaper wraps the route plan's dial connections in
`internal/engine`, driven by a per-profile `[profile.conditioner]` config block
(`internal/config`). It is read and live-updated through additive
`GET`/`PUT /api/v1/conditioner` endpoints (`internal/api/conditioner.go`) and is
surfaced in all four clients (Apple, TUI, Linux, Android).

### Protocol-specific viewers (WebSocket / gRPC / GraphQL) — shipped

Decode and pretty-print application protocols in the HTTP capture detail view:
the WebSocket frame stream, gRPC/protobuf messages, and GraphQL query/response
formatting. Decoding happens daemon-side in `internal/developer/decode` and is
stored as an additive `decoded` field on the capture `Entry`; the Apple, TUI,
Linux, and Android frontends render the shared decoded shape, falling back to the
raw body preview when no decode is available.

### Developer capture tooling parity — shipped (post-v1.1)

Cross-client additions to the HTTP capture surface that bring the inspection
workflow to parity with Proxyman, delivered end-to-end across the daemon and
all four clients (macOS, Linux, TUI, and Android):

- **Server-side flow filtering.** `GET /api/v1/developer/entries` accepts
  `method`, `status_min`, `status_max`, `host`, `scheme`, `content_type`,
  `error_only`, and `q` (free-text search over method/URL/host/chain/status/
  error/headers/body previews). One filter semantics shared by all clients.
- **cURL import/export.** `GET /api/v1/developer/entries/{id}/curl` serializes a
  capture to a runnable cURL command; `POST /api/v1/developer/curl/import` parses
  a pasted cURL command back into the composer via a bounded, stdlib-only shell
  tokenizer (unknown flags best-effort ignored).
- **Standalone compose/send.** `POST /api/v1/developer/send` sends a composed
  request through the capture pipeline independent of any existing capture;
  `repeat` remains for re-sending a captured transaction.
- **Daemon-side body viewer.** A shared `viewer` on each body carries a pretty
  rendering (JSON/XML/form/HTML) and a hex dump, computed once and rendered by
  every client (with a local JSON re-indent fallback for older daemons).
- **HAR 1.2 timings.** The connect/SSL/send/wait/receive breakdown is captured
  around network I/O only (breakpoint and rewrite pauses excluded) and emitted
  in the HAR `timings` block.

These are additive API surface on top of the v1.1 capture pipeline and do not
change the transport set, so they are safe to ship before the Reality milestone.

## Protocol expansion

### Reality transport

VMess and ShadowTLS already ship and are registered in the protocol registry.
Reality is the remaining planned transport and, with v1.1 shipped, is the next
milestone: the capture tooling has stabilized against the current transport set,
so adding another handshake is now safe to sequence.

## Future release candidates

### Surge-style scripting engine

Explicitly deferred in `docs/macos-v1-parity.md`. Revisit after v1.1 and the
Reality transport once rule-based routing, daemon API workflows, and capture
tooling have stabilized.
