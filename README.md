<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# ClambHook

> Privacy-focused VPN and proxy client plus a local HTTP(S) debugging and
> network-inspection tool for macOS (Apple Silicon), GNU/Linux (Ubuntu and
> Fedora), and Android — with SOCKS5, HTTP(S) proxy, WireGuard, OpenVPN,
> Shadowsocks, VMESS, ShadowTLS, Tor, and encrypted DNS (DoH/DoT/DoQ).

ClambHook is a local network-routing, privacy, and developer-inspection client.
Its production runtime is C17. **This repository is the free-software ClambHook
core** (GPL-3.0-only): the runtime library, the `clambhook` daemon and its
control API, and the `clambhook-license` helper. The ClambHook apps — the
SwiftUI macOS client, the Kotlin/Compose Android and GNU/Linux client, and the
`clambhook-tui` terminal client — are proprietary and developed in a separate
private repository; they are built on this core.

## What is ClambHook?

ClambHook is a cross-platform privacy and traffic-routing application for power
users and developers. It combines a VPN and proxy client (SOCKS5, HTTP(S)
proxy, and device-wide TUN routing over WireGuard, OpenVPN, Shadowsocks, VMESS,
ShadowTLS, and Tor) with an opt-in local HTTP(S) capture and debugging
workbench (body viewers, breakpoints, map/rewrite rules, cURL and HAR
export). Profiles, rules, credentials, keys, captures, and diagnostics stay on
the device unless you explicitly export them. Official builds are published only
on the [GitHub Releases page](https://github.com/JohnThre/clambhook/releases)
for Apple Silicon macOS 14+, Ubuntu and Fedora (x86_64 and aarch64), and ARM64
Android 12+.

The completed implementation cutover is recorded in
[outcome details](docs/c-migration.md).

Official binaries appear only on the
[GitHub Releases page](https://github.com/JohnThre/clambhook/releases); a source
version or tag alone is not evidence that an installer has been published.

## Architecture

```mermaid
flowchart TB
    subgraph clients["Product surfaces"]
        mac["macOS 14+<br/>SwiftUI"]
        linux["GNU/Linux (Ubuntu · Fedora)<br/>Kotlin · Compose Desktop"]
        android["Android 12+ ARM64<br/>Kotlin · Compose"]
        tui["C terminal UI"]
    end

    subgraph platform["Platform ownership"]
        macHelper["macOS signed helper<br/>utun · routes · DNS"]
        linuxServices["GNU/Linux services<br/>systemd · polkit · secret-tool"]
        kotlin["Kotlin platform AAR<br/>VpnService · consent · files · QR<br/>secure storage · notifications · updater"]
    end

    subgraph runtime["C17 production runtime"]
        control["Control and event boundary<br/>HTTP/WebSocket or JNI"]
        config["TOML config · profiles<br/>transaction + rollback"]
        routing["Rules · rule sets · subscriptions<br/>policy groups · prompts"]
        network["SOCKS5 · HTTP(S) · TUN<br/>DNS · firewall · conditioner"]
        protocols["Protocol and chain engine<br/>WireGuard · OpenVPN · VMESS<br/>ShadowTLS · Shadowsocks · Tor"]
        developer["Capture · CA · map · rewrite<br/>breakpoints · cURL · HAR"]
        support["Traffic · events · geo<br/>license · persistence · updates"]
        lwip["lwIP packet stack"]
        crypto["OpenSSL · libsodium · libuv<br/>libcurl · llhttp"]
    end

    mac -->|authenticated loopback| control
    mac --> macHelper
    linux -->|authenticated loopback| control
    linux --> linuxServices
    android --> kotlin -->|JNI| control
    tui -->|authenticated loopback| control
    control --> config
    control --> routing
    control --> developer
    routing --> network --> protocols
    protocols --> lwip
    protocols --> crypto
    control --> support
    macHelper --> control
    linuxServices --> control
```

The shared Kotlin layer of the apps is split deliberately:

- `RuntimeClient` is a typed, asynchronous view of the frozen control and event
  contracts.
- `PlatformServices` owns platform-only behavior: VPN consent and lifecycle,
  files, QR, secure storage, clipboard/browser integration, notifications,
  licensing, updates, and Android per-application routing.
- On Android, `ClambhookVpnService` owns the single C runtime. Closing the
  Compose activity never destroys it.
- On GNU/Linux, the Compose Desktop controller communicates with the supervised
  C daemon over authenticated loopback HTTP and WebSocket endpoints.

## Runtime data flow

```mermaid
sequenceDiagram
    participant User
    participant UI as Kotlin/Compose or SwiftUI client
    participant Platform as Kotlin AAR or desktop services
    participant API as C17 control/event boundary
    participant Router as C17 policy and chain engine
    participant Tunnel as lwIP / TUN / protocol transport
    participant Store as Traffic, events, config, license

    User->>UI: Connect or edit configuration
    UI->>Platform: Request consent / platform operation
    Platform->>API: Attach to service or supervised daemon
    UI->>API: Typed request over loopback or JNI
    API->>Router: Validate and apply transaction
    Router->>Tunnel: Route TCP/UDP packet or stream
    Tunnel-->>Router: Result, counters, errors
    Router->>Store: Persist metadata and publish event
    Store-->>UI: WebSocket event / refreshed document
    UI-->>User: Status, traffic, prompt, or retry state
```

Configuration changes preserve the public TOML and JSON contracts and are
validated before atomic persistence. Listener, profile, rule, DNS, policy, and
developer changes roll back if activation fails.

## Network and protocol support

- SOCKS5, HTTP proxy, HTTPS interception when explicitly enabled, and
  device-wide TUN routing.
- Multi-hop chains and `select` or `url-test` policy groups.
- WireGuard over TCP/UDP routes with DNS, peer keys, allowed IPs, keepalive,
  MTU, replay protection, rekey, and lifecycle handling.
- OpenVPN 2.6+ UDP subset with TLS 1.2+, key-method 2/TLS-EKM,
  AES-256-GCM or ChaCha20-Poly1305, PKI, and optional username/password.
  TCP transport, CBC, compression, and legacy control modes are rejected.
- VMESS-AEAD, ShadowTLS v3, Shadowsocks AEAD-2018, Tor SOCKS5 isolation,
  direct routing, blocking, prompting, and chained transports.
- Encrypted DNS using DoH, DoT, or DoQ; local TUN answering and Control D
  resolver identifiers are supported.
- IPv4/IPv6 packet handling, fragmentation, flow/session tracking, tuple and
  checksum restoration, encrypted-DNS interception, and domain recovery use
  the shared lwIP-backed C packet stack.

## User features

- Connection state, profiles, import/export, QR workflows, servers, chains,
  activity, traffic, and routing decisions.
- Rules, temporary rules, policy groups, rule sets, remote subscriptions, and
  interactive prompts.
- DNS, firewall/TUN settings, capture, network conditioner, process matching,
  and per-application Android routing.
- Opt-in HTTP(S) capture with body viewers, filtering, timing, HAR export,
  repeat/send, cURL import/export, map rules, rewrite rules, and breakpoints.
- Licensing, update status, accessible keyboard navigation, focus semantics,
  responsive layouts, and background-thread-safe refresh/retry handling.

## Platforms

| Platform | Product UI | Runtime and packaging |
| --- | --- | --- |
| macOS 14+ Apple Silicon | SwiftUI | Bundled and signed C17 daemon and TUI; notarized DMG |
| Ubuntu 24.04 LTS and Fedora 44, x86_64 and aarch64 (the only supported GNU/Linux distributions) | Kotlin 2.4 / Compose Multiplatform 1.11 | `clambhook` (GPL core: daemon, license helper, systemd unit) and `clambhook-ui` (desktop controller with a private jlink runtime, TUI); GPG-signed `.deb`/`.rpm` and signed apt/dnf repositories |
| Android 12+ ARM64 | Kotlin 2.4 / Compose Multiplatform 1.11 | Kotlin platform library, JNI C runtime, signed APK and AAB; application ID `org.jpfchang.clambhook`, minSdk 31, targetSdk 36 |
| Terminal | C17 | `clambhook` and `clambhook-license` (core); `clambhook-tui` (apps) |

Windows development is discontinued with no planned resumption date.

## Build and test

The core build needs CMake 3.22+, Ninja, a C17 compiler, `pkg-config`,
OpenSSL 3, libsodium, libuv, and libcurl. The pinned llhttp parser is compiled
from `third_party/llhttp/`.

| Command | Purpose |
| --- | --- |
| `make build-native` | Build the production C17 daemon and license helper. |
| `make test-native` | Run strict C tests under ASan/UBSan and the frozen license contract. |
| `make lint` | Run license/cutover checks, shell checks, and the warning-as-error C build. |
| `make package-smoke` | Validate the core package payload and recipes. |
| `make ci-local` | Run the applicable local mirror of hosted CI. |

See [Outline access keys](docs/outline-access-keys.md),
[Mihomo and Surge profile conversion](docs/profile-conversion.md),
[release validation](docs/release-validation.md), and
[packaging](packaging/README.md).

## CI, packages, and release

```mermaid
flowchart LR
    source["Signed source commit"] --> policy["Policy gates<br/>SPDX · shell · actionlint<br/>zero retired sources"]
    policy --> ctest["C17 strict + ASan/UBSan<br/>contract and protocol fixtures"]
    policy --> macos["C17 runtime on macOS"]
    ctest --> linux["GNU/Linux x86_64 + aarch64<br/>Ubuntu 24.04 · Fedora 44<br/>core package install/uninstall"]
    linux --> local["Maintainer release build<br/>GPG-sign · notarize · verify"]
    local --> releases["Versioned GitHub Release<br/>scripts/publish-release.sh"]
```

CI builds and tests only; no workflow builds, signs, or publishes installers.
Releases — the core packages here and the app installers from the private apps
repository — are built and signed on the maintainer's machine, checked with
`scripts/verify-release-signatures.sh`, and uploaded by
`scripts/publish-release.sh`. See [GitHub CI/CD](docs/github-cicd.md).

## Distribution and licensing

When available, official downloads are hosted only at
<https://github.com/JohnThre/clambhook/releases>. Each release
publishes a notarized DMG for Apple Silicon Macs running macOS 14 or later,
signed ARM64 Android packages, and signed Ubuntu and Fedora packages. Every
installer, checksum, manifest, and apt/dnf repository index carries a
signature from the developer@jpfchang.org release key
(`BAFC 7769 FDA1 E0D4 EBD2 3E2F 6FF4 807E AD97 7A9B`), which is verified
before upload; see [release signing](docs/website-release/signing.md). If the page has
no release, no official binary has been published yet; build the core locally
or wait for a release rather than obtaining an installer elsewhere.

The commercial product contract is:

- a 7-day trial for new installations; already-started month-long trials are grandfathered;
- a recurring USD 79.99 annual subscription;
- releases published during each paid term;
- versions released on or before the paid-through cutoff remain usable after cancellation or lapse (a perpetual compatible fallback);
- a maximum of 6 concurrently active devices;
- seats can be deactivated and transferred;
- cancellation stops future billing without revoking the paid term;
- resubscription can reuse the same provider-neutral license key;
- checkout uses Creem or NOWPayments, not PayPal.

```mermaid
stateDiagram-v2
    [*] --> Trial
    Trial --> Active: verified annual payment or key activation
    Trial --> TrialEnded: seven days elapse
    TrialEnded --> Active: verified annual payment or key activation
    Active --> Active: annual renewal
    Active --> Deactivated: deactivate seat
    Deactivated --> Active: reactivate this seat
    Active --> Transferred: transfer frees this seat
    Transferred --> Active: activate destination device
    Active --> Fallback: cancel or paid term lapses
    Fallback --> Active: resubscribe with the same key

    note right of Active
        C17 license helper evaluates the signed snapshot
        Kotlin, SwiftUI, and Android platform services share the result
        Maximum 6 concurrently active devices
    end note
```

## Support independent development

Donations are separate from ClambHook subscriptions. They never create a
license, extend a paid term, grant a supporter badge, or change support
priority. You can donate through
[Ko-fi](https://ko-fi.com/jpfchang),
[Liberapay](https://en.liberapay.com/jpfchang/), or
[IssueHunt](https://oss.issuehunt.io/u/johnthre).

<a href="https://nowpayments.io/donation?api_key=4f798f1e-c93e-456e-8067-b03b200790cd" target="_blank" rel="noreferrer noopener" referrerpolicy="no-referrer">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://nowpayments.io/images/embeds/donation-button-white.svg">
    <img src="https://nowpayments.io/images/embeds/donation-button-black.svg" width="213" height="60" alt="Crypto donation button by NOWPayments">
  </picture>
</a>

The ClambHook core in this repository is GPL-3.0-only, with separate written
commercial terms available; `clib/**` is Apache-2.0. The ClambHook apps are
proprietary; app versions 1.0.2 and earlier were published under GPL-3.0-only
and remain available under it. Pinned third-party material retains
its upstream licenses and provenance. See [licensing](LICENSING.md),
[notice](NOTICE), and [third-party notices](THIRD_PARTY_NOTICES.md).

## Security and contribution

Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).
Contributions require the agreement in [CLA.md](CLA.md), SPDX headers, tests,
and preservation of the published control, persistence, identifier, and
licensing contracts.

## Documentation map

- [Roadmap](docs/roadmap.md) and [project review](docs/project-review.md):
  delivered architecture, current priorities, and reviewed boundaries.
- [Release validation](docs/release-validation.md),
  [GitHub CI/CD](docs/github-cicd.md), and
  [packaging](packaging/README.md): release evidence and artifact policy.
- [Distribution](docs/distribution.md), [licensing](LICENSING.md), and
  [security](SECURITY.md): public distribution, legal boundaries, and private
  vulnerability reporting.

## Frequently asked questions

### What platforms does ClambHook support?

Apple Silicon Macs running macOS 14 or later (native SwiftUI app), GNU/Linux on
Ubuntu and Fedora for x86_64 and aarch64 (Kotlin/Compose Desktop app), and
ARM64 Android 12+ (Kotlin/Compose app). Other GNU/Linux distributions are not
supported. A C terminal UI is also
provided. Windows development is discontinued.

### Which protocols and features does ClambHook support?

SOCKS5, HTTP(S) proxy, device-wide TUN routing, WireGuard, OpenVPN 2.6+,
Shadowsocks, VMESS-AEAD, ShadowTLS v3, Tor, multi-hop chains, policy groups,
and encrypted DNS over DoH, DoT, or DoQ. It also includes an opt-in local
HTTP(S) capture and debugging workbench with body viewers, filtering, timing,
breakpoints, map rules, rewrite rules, repeat/compose, cURL import/export, and
HAR export.

### Is ClambHook free? What does it cost?

New installations get a 7-day trial, after which a recurring USD 79.99 annual
subscription is required. One subscription covers a maximum of 6 concurrently
active devices, seats can be deactivated and transferred, and versions released
on or before your paid-through cutoff remain usable after cancellation.
Checkout uses Creem or NOWPayments.

### Is ClambHook open source?

The core is. The runtime, daemon, control API, and license helper in this
repository are GPL-3.0-only (`clib/**` is Apache-2.0), and separate written
commercial terms are available. The macOS, Android, and GNU/Linux apps and the
terminal client are proprietary from version 1.1.0; earlier app releases remain
GPL-3.0-only. See [licensing](LICENSING.md).

### Where do I download official builds?

Official, signed builds are published only on the
[GitHub Releases page](https://github.com/JohnThre/clambhook/releases). A source
version or tag alone is not evidence that an installer has been published.

### How can I support ClambHook?

Donations are optional and separate from subscriptions. You can support the
project through [Ko-fi](https://ko-fi.com/jpfchang), [Liberapay](https://en.liberapay.com/jpfchang/),
[IssueHunt](https://oss.issuehunt.io/u/johnthre), or NOWPayments.

## Author

Pengfan Chang — <support@swiphtgroup.com>
