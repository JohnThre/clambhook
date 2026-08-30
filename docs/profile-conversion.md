<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Mihomo and Surge Profile Conversion

ClambHook can review and convert a Mihomo YAML document or a Surge profile
without network access. The converter never expands includes, reads sidecars,
interpolates environment variables, resolves DNS, or downloads proxy and rule
providers. A review contains a SHA-256 digest of the validated TOML; import
reruns conversion and refuses the operation if that digest changed.

Converted TOML contains proxy credentials. Treat reviews and exports as
sensitive files even though summaries, warnings, errors, and TUI output omit
credentials.

## Compatibility

| Source feature | Conversion |
| --- | --- |
| Shadowsocks | `aes-128-gcm`, `aes-256-gcm`, and `chacha20-ietf-poly1305` |
| VMess | AEAD (`alterId = 0`) over raw TCP, with supported TLS settings |
| Trojan | Raw TCP with supported TLS settings |
| ShadowTLS | Version 3 when representable as an acyclic carrier hop |
| `dialer-proxy` / `underlying-proxy` | Acyclic carrier chains |
| `select` | ClambHook policy group |
| `url-test` / Surge `smart` | ClambHook `url-test` group |
| `fallback` and load balancing | Usable members retained with a semantic-loss warning |
| Domain, suffix, keyword, CIDR, source-CIDR, process, network, port, final rules | Ordered rules, when the value and action are representable |

Unsupported protocols and transports are omitted with path-specific warnings.
These include legacy VMess, WebSocket, gRPC, Reality, unsupported Shadowsocks
plugins, HTTP/SOCKS outbound proxies, Snell, VLESS, TUIC, Hysteria, AnyTLS,
SSH, Tailscale, and MASQUE. Conversion fails when no routable chain remains.
Port ranges and unrecognized rule types are also omitted.

Provider references remain offline. Remote proxy providers are not converted.
Remote rule providers and formats that cannot be represented safely produce a
warning rather than an implicit fetch. Ambiguous mixed listeners, system-DNS
policy behavior, fake IP, scripts, MITM, rewrites, and platform-only settings
are not copied. DNS and TUN source sections are reported in review when their
behavior cannot be preserved safely.

## Workflow

The macOS app accepts `.yaml`, `.yml`, and Surge `.conf` files in the converter
panel. The GNU/Linux and Android JavaFX view supports paste or the platform file
adapter. In the C TUI, press `v`, select a source path, review the sanitized
counts and warnings, and choose merge or sensitive TOML export. Activation is
off by default; merge preserves unrelated profiles and root settings and uses
the normal backup, validation, reload, and rollback transaction.

The authenticated API accepts JSON up to the configuration-transfer limit:

- `POST /api/v1/config/converter/review` with `source`, `format` (`auto`,
  `mihomo`, or `surge`), and `profile_name`.
- `POST /api/v1/config/converter/import` with the same fields plus
  `expected_sha256` and optional `activate`.

Direct ClambHook TOML uses the same non-destructive transaction through
`POST /api/v1/config/import/review` (TOML body) followed by
`POST /api/v1/config/import/apply` (selected source and target profile names).

The review response includes detected `format`, sanitized profile counts,
structured `warnings`, canonical sensitive `toml`, and `sha256`. Each warning
has a stable `code`, source `path`, and review message; `path` identifies the
source item that was omitted or mapped with reduced semantics.

The mapping follows the terminology in the current
[Mihomo configuration documentation](https://wiki.metacubex.one/en/config/) and
[Surge profile documentation](https://manual.nssurge.com/profile/format.html).
