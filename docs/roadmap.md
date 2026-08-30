<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Roadmap

## Delivered foundation

- C17 daemon, protocol/chain engine, packet stack, API/WebSocket server,
  configuration/persistence, developer tools, terminal UI, and license helper.
- Shared JavaFX 21.0.12 / GluonFX 1.0.29 application for Android and GNU/Linux.
- Kotlin-only Android platform AAR with service-owned runtime and ARM64
  APK/AAB output at the API 31 floor.
- SwiftUI macOS 14+ Apple Silicon client using only the C runtime.
- GNU/Linux x86_64/aarch64 native images and Ubuntu/Fedora packaging matrix.
- WireGuard, OpenVPN UDP/TLS-EKM, VMESS-AEAD, ShadowTLS, Shadowsocks, Tor,
  encrypted DNS, rules/policy/prompts, capture, and traffic/event contracts.
- Protected multi-platform CI plus signing, artifact inspection, and GitHub
  Release automation. A workflow is delivered only when its published assets
  are independently downloaded and verified.

## Current priorities

1. Publish and independently verify the first complete protected release for
   macOS, GNU/Linux, and Android, including immutable downloads and signatures.
2. Expand real-world interoperability fixtures for WireGuard and OpenVPN peers
   while preserving deterministic packet/control tests.
3. Extend accessibility regression automation across more desktop screen
   readers and Android form factors.
4. Add richer capture body diffing and export filters without changing the
   frozen API route shapes.
5. Improve rule and policy explainability with traceable decision graphs.
6. Reduce native-image build time and cache size while retaining reproducible
   checksums and complete JNI/reflection metadata.
7. Add signed repository metadata for GNU/Linux update discovery without
   bundling a package manager or Java runtime.

## Non-goals

- API 30 compatibility or 32-bit Android release packages.
- A bundled JRE on GNU/Linux.
- Windows support.
- Apple platforms other than macOS.
- Automatic cloud traffic capture or upload; capture remains local and opt-in.
- Relaxing certificate, update, license, signing, or source provenance checks.

Public contract changes require a versioned design record, compatibility
fixtures, migration notes, and all platform gates before implementation.
