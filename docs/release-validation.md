<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Release validation

This document defines the evidence required before and after a ClambHook
release. It is also the continuing regression checklist for the C17 core. The
app gates (Kotlin/Compose, Android, macOS) live in the private apps repository. Passing source checks does not prove that public
release assets exist.

## Validation topology

```mermaid
flowchart TD
    source["Source tree"] --> policy["Source-only · SPDX · secrets<br/>workflow + cutover policy"]
    policy --> native["C17 strict build<br/>ASan/UBSan · CTest"]
    native --> protocols["Deterministic fixtures<br/>real WireGuard/OpenVPN peers"]
    native --> contracts["TOML · JSON · HTTP · WebSocket<br/>rollback · CLI · license"]
    native --> distros["Ubuntu 24.04 · Fedora 44<br/>core package install · uninstall"]
    protocols --> ready["Source readiness"]
    contracts --> ready
    distros --> ready
    apps["App gates<br/>private apps repository"] --> ready
    ready --> local["Local build · inspection<br/>GPG signing + verification · notarization"]
    local --> published["Versioned GitHub Release"]
    published --> download["Independent download<br/>hash · signature · install · update smoke"]
```

## C17 runtime gate

```sh
make test-native
```

The native gate uses `-Wall -Wextra -Wpedantic -Wconversion -Wshadow -Werror`
and ASan/UBSan. It covers:

- configuration defaults, imports/exports, mutation transactions, persisted
  backups, activation rollback, and malformed/oversized input;
- authenticated HTTP routes, WebSocket upgrade/framing/filtering, reconnect,
  event ordering, status/error envelopes, and API token handling;
- SOCKS5, HTTP forwarding, HTTPS interception, TUN packet handling, IPv4/IPv6,
  TCP/UDP, fragmentation, process attribution, and network changes;
- rule, policy, rule-set/subscription, prompt, temporary-rule, conditioner,
  DNS, geo, traffic, capture, CA, map/rewrite, breakpoint, and persistence
  behavior;
- WireGuard handshake/data, TCP/UDP routing, replay, rekey, keepalive, MTU,
  lifecycle, peer keys, and allowed IPs;
- OpenVPN UDP/TLS-EKM, AEAD data, PKI/user-password, malformed packets, replay,
  and explicit rejection of unsupported transport/crypto/control modes;
- VMESS-AEAD, ShadowTLS, Shadowsocks, Tor, direct, nested-chain, and policy
  selection fixtures;
- exact CLI version behavior and frozen license-helper JSON hashes.

The authoritative GNU/Linux protocol lane provisions real kernel WireGuard and
OpenVPN 2.6 loopback peers, exercises WireGuard TCP/UDP echo and an OpenVPN UDP
TLS-EKM data path, and fails if TUN privileges or peer tooling are unavailable.
The deterministic packet/control fixtures remain mandatory and are never
replaced by the peer smoke.

## GNU/Linux gate

```sh
scripts/validate-linux-distros.sh
make package-smoke
```

Both x86_64 and aarch64 lanes:

- build/test C17;
- install the core daemon and license helper;
- validate systemd/sysusers/tmpfiles integration;
- install the native package, launch its C daemon against the authenticated
  loopback API, and then remove the package while checking that all payload
  registrations disappear;
- reject client payload (desktop controller, TUI, Java runtime) in the core
  package;
- exercise the daemon and license helper;
- inspect architecture, dynamic dependencies, notices, and SBOM inputs;
- uninstall cleanly and verify package-owned paths are gone.

Ubuntu produces Debian artifacts and Fedora produces RPM artifacts. They are
the only authoritative GNU/Linux validation targets.

## Artifact and source gate

Before staging:

```sh
scripts/check-source-only.sh .
scripts/check-cutover.sh
scripts/check-license-policy.sh
scripts/check-github-actions.sh
git diff --check
```

After `git add -A`:

```sh
git diff --cached --check
git diff --cached --stat
git ls-files '*.go'
git ls-files go.mod go.sum vendor
```

The last two commands must print nothing. Inspect all packages to require:

- in `clambhook`: no client payload and no Java runtime; in `clambhook-ui`: no system JRE/JDK dependency, no Java runtime outside `/usr/lib/clambhook/ui`, and no JavaFX/Gluon/GTK payload;
- no retired runtime build-information section;
- no migration guards or suffixed executable names;
- only ARM64 native libraries in Android APK/AAB;
- correct application/bundle identifiers and API floors;
- correct C17 executables, desktop distributable, licenses, notices, and update
  manifests;
- valid developer@jpfchang.org signatures on every installer, checksum,
  manifest, and repository index (`scripts/verify-release-signatures.sh`), plus
  embedded `rpm -K` and `debsig-verify` signatures and a signed-repository
  install on Ubuntu and Fedora (`scripts/test-linux-repo-install.sh`).

## Source delivery gate

Create one coherent source commit, push non-force to `origin/master`, and
monitor every required workflow. Fix defects with follow-up commits until all
required CI/security jobs pass. Do not create a release tag or manually publish
artifacts for source delivery.

Finish only when:

```sh
test "$(git rev-parse HEAD)" = "$(git rev-parse origin/master)"
test "$(git rev-parse HEAD)" = "$(git ls-remote origin refs/heads/master | awk '{print $1}')"
test -z "$(git status --porcelain)"
```

## Publication gate

After `scripts/publish-release.sh --publish` completes, download every selected-platform asset
from its immutable versioned URL. Verify SHA-256 records, GPG signatures, APK
and AAB signatures, Developer ID/notarization/stapling, Sparkle signatures, and
manifest URLs before calling the release public. Install, launch, update-check,
and uninstall smoke tests must use the downloaded bytes rather than workspace
artifacts. A release page created before every selected platform's assets are uploaded is
not a completed release.
