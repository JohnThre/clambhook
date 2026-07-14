# ClambHook Roadmap

Committed direction beyond the current macOS v1 release. Items here are planned
work, not shipped features. See `docs/macos-v1-parity.md` for what ships in v1.

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

## Future release candidates

### Surge-style scripting engine

Explicitly deferred in `docs/macos-v1-parity.md`. Revisit after v1.1 once
rule-based routing, daemon API workflows, and capture tooling have stabilized.
