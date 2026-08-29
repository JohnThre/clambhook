<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Security architecture review

This document records the current C17/JavaFX security architecture. It is not a
claim that future versions are vulnerability-free. Vulnerabilities must be
reported privately under [SECURITY.md](../SECURITY.md).

## Trust boundaries

1. Untrusted network traffic enters SOCKS5, HTTP(S), DNS, TUN, and protocol
   parsers in the C17 runtime.
2. Local clients reach only the authenticated loopback HTTP/WebSocket server.
3. HTTPS capture is disabled by default and uses a user-approved local CA.
4. Android system privileges terminate at the Kotlin AAR and
   `ClambhookVpnService`; JavaFX does not own the TUN descriptor.
5. GNU/Linux device-wide routing terminates at the hardened systemd service and
   polkit policy.
6. macOS privileged routing terminates at the signed helper/LaunchDaemon.
7. License/update responses and release artifacts cross a cryptographic trust
   boundary before installation or activation.

## Control API and input handling

- API tokens are generated outside persisted TOML and compared without
  data-dependent early exit.
- Host/Origin and loopback checks mitigate DNS rebinding and cross-site
  WebSocket use.
- HTTP request lines, headers, bodies, WebSocket frames, JSON, TOML, capture
  bodies, rule feeds, and helper output have explicit limits.
- Configuration writes use restrictive permissions, validation, atomic rename,
  backup, and runtime rollback.
- Path identifiers are encoded by clients and validated by handlers.
- Error envelopes avoid returning secrets; logs and capture exports apply the
  configured redaction policy.

## Network and cryptography

- TLS verification is enabled for ordinary upstreams. The capture CA is used
  only for explicit local interception and is stored with restrictive
  permissions.
- OpenSSL and libsodium provide maintained primitives; protocol-specific legacy
  constructions are limited to interoperability requirements and covered by
  fixed fixtures.
- WireGuard replay windows, counters, rekey thresholds, peer keys, allowed IPs,
  and keepalive are validated in C tests.
- OpenVPN accepts only UDP, TLS 1.2+, key-method 2/TLS-EKM, and AEAD suites.
  TCP, CBC, compression, and legacy control modes fail closed.
- Packet length arithmetic, fragments, extension headers, checksums, DNS
  compression, and flow/session state are bounded and sanitizer-tested.
- Remote rule/subscription and developer requests enforce redirect, address,
  protocol, size, and timeout policy.

## Privilege and platform handling

- The GNU/Linux daemon runs as a dedicated user with only
  `CAP_NET_ADMIN`/`CAP_NET_RAW`, a closed device policy, a restricted address
  family set, protected filesystem/kernel surfaces, and managed config/state
  directories.
- Android obtains VPN consent before TUN start, uses the required foreground
  service types, stores secrets through encrypted platform storage, and keeps
  update files in a non-exported FileProvider.
- macOS validates helper identity and bundle paths; release checks reject
  unexpected architectures, absolute package-manager library paths, and
  unapproved extension payloads.
- External commands are invoked with argument arrays, bounded output, timeouts,
  fixed executable roles, and validated user-derived values.

## Licensing and updates

- The C license helper evaluates a versioned signed snapshot and returns a
  bounded JSON result. Device-seat and update-cutoff rules are identical across
  JavaFX, Kotlin, and SwiftUI surfaces.
- Android checks update metadata/hash/signature before install handoff.
- GNU/Linux updates remain package-repository managed.
- macOS requires manifest hash, GPG signature, Sparkle signature, Developer ID,
  notarization, and stapling.
- Protected workflows keep Android, GPG, Apple, and Sparkle private keys in
  runner-temporary files with restrictive modes and remove them afterward.

## Supply chain

- Third-party actions use immutable commit SHAs.
- actionlint and GraalVM archives use pinned SHA-256 values.
- Android OpenSSL/curl archives are checksum-pinned.
- C dependencies retained in `third_party/` include provenance and license
  records.
- Maven/Gradle lock versions are explicit; dependency review, CodeQL, SPDX,
  source-only, and package-payload gates run in CI.
- Installer/package outputs are prohibited from ordinary CI and are published
  only by the protected release workflow.

## Verification

```sh
make test-native
make test-javafx
make test-android
scripts/check-license-policy.sh
scripts/check-cutover.sh
scripts/check-github-actions.sh
scripts/validate-systemd-unit.sh
```

Release validation additionally runs managed Android journeys, both GNU/Linux
architectures and all three distros, SwiftUI build/tests, package install and
uninstall, archive inspection, signature verification, and required CodeQL and
dependency-review jobs.
