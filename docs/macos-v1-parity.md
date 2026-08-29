<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# macOS v1 parity

The macOS 14+ Apple Silicon SwiftUI client is fully attached to the C17 runtime.
This checklist is a release contract, not a migration tracker.

## Runtime and routing

- [x] Bundled arm64 `clambhook` daemon and `clambhook-tui`.
- [x] System Proxy mode using local HTTP and SOCKS5 listeners.
- [x] Enhanced mode using the privileged helper, utun, routes, and reversible
  DNS changes.
- [x] Profile activation, triggers, status, traffic counters, routing
  decisions, and listener lifecycle through authenticated loopback contracts.
- [x] WireGuard, OpenVPN UDP subset, VMESS-AEAD, ShadowTLS, Shadowsocks, Tor,
  direct, block, prompt, and multi-hop chains.
- [x] Transactional TOML import/export, settings, rule/policy/rule-feed
  mutation, validation, backup, and rollback.

## SwiftUI product surface

- [x] Dashboard, profiles, servers/chains, activity, decisions, rules,
  policies, prompts, DNS/network, settings, licensing, and updates.
- [x] Opt-in HTTP(S) capture, certificate management, filters, body viewers,
  HAR, repeat/send, cURL import/export, map/rewrite rules, and breakpoints.
- [x] Menu bar, widgets, recovery state, device-seat management, and strict
  update-cutoff messaging.
- [x] Keyboard/focus/accessibility semantics and failure/retry presentation.

## Packaging and release

- [x] Apple Silicon only, deployment target macOS 14.
- [x] Bundle-relative libsodium, OpenSSL, libuv, and llhttp dependencies.
- [x] Developer ID signing, hardened runtime, notarization, stapling, and
  Gatekeeper verification.
- [x] DMG/ZIP, SHA-256, GPG signatures, signed Sparkle appcast, and update
  manifest agree on version/build.
- [x] No unexpected Network/System Extension or mobile framework payload.

Validate with:

```sh
make build-apple
make test-apple
make macos-release-contract-check
scripts/check-macos-signing.sh /path/to/ClambhookMac.app
```

See [scope](macos-v1-scope.md) for the supported routing modes and limits.
