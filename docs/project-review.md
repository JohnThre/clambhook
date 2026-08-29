<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Project review

## Current architecture

ClambHook has one C17 runtime and one shared JavaFX/Gluon application for
Android and GNU/Linux. Android retains a Kotlin platform AAR; macOS retains
SwiftUI. The public CLI, TOML, JSON, HTTP/WebSocket, persistence, licensing,
identifier, and release contracts are frozen.

## Reviewed implementation surfaces

### C17 core

- strict-warning CMake targets and sanitizer-backed CTest;
- bounded JSON/TOML/HTTP/WebSocket parsing and authenticated loopback control;
- transactional config mutation, backup, activation rollback, and permission
  handling;
- SOCKS5/HTTP/TUN listeners, lwIP packet flows, encrypted DNS, network
  observation, and process attribution;
- protocol/chain behavior including WireGuard and the supported OpenVPN subset;
- rule/policy/rule-feed, prompts, traffic/events, geo, licensing, and developer
  capture state;
- production daemon/TUI/license executable names and install components.

### Platform clients

- typed asynchronous `RuntimeClient` route and event mapping;
- `PlatformServices` separation and background-thread boundaries;
- JavaFX responsive navigation, keyboard/focus/accessibility state, and
  mutation failure/retry behavior;
- Android service ownership, consent, foreground lifecycle, process restart,
  per-application routing, secure storage, QR/files, notifications, licensing,
  and updates;
- SwiftUI helper/runtime handoff and bundle-relative C dependencies.

### Build, package, and release

- Java 17, JavaFX 21.0.12, Gluon static substrate 21.0.1, and GluonFX 1.0.29;
- Maven tests/coverage, Gradle Kotlin AAR, CMake/CTest, and standalone pinned
  workflow linting;
- x86_64/aarch64 Ubuntu/Fedora lanes and API 31/33/36 ARM64 Android
  managed devices;
- source-only, license-boundary, stale-reference, package payload, signing,
  checksum, and update-manifest gates.

## Review conclusions

- The production tree has a single runtime source of truth.
- JavaFX is the only Android/GNU/Linux product UI.
- Android activity lifecycle cannot destroy the service-owned runtime.
- GNU/Linux packages contain a self-contained native UI and no bundled JRE.
- macOS embeds the C runtime and remains Apple Silicon/macOS 14+ only.
- Retired module, binding, and UI sources are absent from the delivered tree.
- Ubuntu produces Debian files and Fedora produces RPM files; no other
  GNU/Linux distribution is an authoritative test target.

## Ongoing review items

- Keep external WireGuard/OpenVPN interoperability peers in addition to
  deterministic fixtures.
- Re-run accessibility journeys when JavaFX/Gluon or Android system images
  change.
- Inspect every release archive after toolchain upgrades.
- Treat any control-route, persistence, identifier, license, or update-manifest
  change as a compatibility change requiring new fixtures and documentation.

The authoritative evidence and commands are in
[release validation](release-validation.md) and the completed
[cutover record](c-migration.md).
