# wireguard-lwip provenance

- Upstream: https://github.com/smartalock/wireguard-lwip
- Revision: `c54f20dbe76ac8b3411ad21e0ed7deea6f0cfd4d`
- Retrieved: 2026-08-28
- License: BSD-3-Clause; see `LICENSE` and the X25519 notice in
  `src/crypto/refc/x25519-license.txt`.

ClambHook vendors the protocol and reference-cryptography files only. The
upstream `wireguardif` lwIP adapter is deliberately excluded: ClambHook uses
its own POSIX/Android UDP transport, allowed-IP routing, and multi-interface
inner-stack bridge. Local integration changes are limited to capacity and
portability settings in `wireguard-platform.h`; upstream copyright headers
remain intact.
