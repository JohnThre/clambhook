# ClambHook Roadmap

Committed direction beyond the current macOS v1 release. Items here are planned
work, not shipped features. See `docs/macos-v1-parity.md` for what ships in v1.

## Sequencing

The `docs/project-review.md` release and security blockers are closed, so the
post-v1 work is unblocked and sequenced as:

1. **v1.1 — capture and network tooling.** Network throttling / conditioner,
   then the protocol-specific viewers. Both build on the already-shipping daemon
   chain and HTTP capture pipeline and touch only additive API surface, so they
   carry the least cross-client risk.
2. **Protocol expansion — Reality.** VMess and ShadowTLS transports already ship
   (`internal/protocol/vmess`, `internal/protocol/shadowtls`, both registered in
   the protocol registry). Reality is the remaining planned transport; sequence
   it after v1.1 so capture tooling stabilizes against the current transport set
   first.
3. **Scripting engine.** A future release candidate only, gated behind the
   parity-doc deferral below.

Each step starts only after the previous one ships, keeping one cross-client
change in flight at a time.

## v1.1

### Network throttling / conditioner

Simulate constrained networks on the daemon chain path: bandwidth caps, added
latency/jitter, and packet-loss simulation, toggled per active profile.
Implemented in the daemon since all traffic already flows through the chain and
listeners, then exposed through the daemon API and surfaced in the UIs.

### Protocol-specific viewers (WebSocket / gRPC / GraphQL)

Decode and pretty-print application protocols in the HTTP capture detail view:
the WebSocket frame stream, gRPC/protobuf messages, and GraphQL query/response
formatting. Requires extending the HTTP capture pipeline to surface frames and
adding viewers across the Apple, TUI, Linux, and Android frontends.

## Protocol expansion

### Reality transport

VMess and ShadowTLS already ship and are registered in the protocol registry.
Reality is the remaining planned transport. Sequence it after v1.1 so the
capture tooling stabilizes against the current transport set before adding
another handshake.

## Future release candidates

### Surge-style scripting engine

Explicitly deferred in `docs/macos-v1-parity.md`. Revisit after v1.1 and the
Reality transport once rule-based routing, daemon API workflows, and capture
tooling have stabilized.
