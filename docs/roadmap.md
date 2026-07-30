# ClambHook Roadmap

Committed direction beyond the current macOS v1 release. Items here are planned
work, not shipped features. See `docs/macos-v1-parity.md` for what ships in v1.

## Sequencing

The `docs/project-review.md` release and security blockers are closed, so the
post-v1 work is unblocked and sequenced as:

1. **v1.1 — capture and network tooling. Shipped.** The network throttling /
   conditioner and the protocol-specific viewers are delivered end-to-end across
   the daemon and all four clients. Both built on the already-shipping daemon
   chain and HTTP capture pipeline and touched only additive API surface.
2. **Protocol expansion — Reality.** *Next up.* VMess and ShadowTLS transports
   already ship (`internal/protocol/vmess`, `internal/protocol/shadowtls`, both
   registered in the protocol registry). Reality is the remaining planned
   transport; now that v1.1 has shipped and capture tooling is stable against the
   current transport set, this is the next milestone.
3. **Scripting engine.** A future release candidate only, gated behind the
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
