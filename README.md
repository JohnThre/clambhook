<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# ClambHook

ClambHook is a local network-routing, privacy, and developer-inspection client.
Its production runtime is C17. Android and GNU/Linux share one JavaFX
application built with Gluon; macOS keeps its native SwiftUI client. A C daemon,
C terminal UI, and C license helper provide the command-line surface.

The completed implementation cutover is recorded in
[outcome details](docs/c-migration.md).

## Architecture

```mermaid
flowchart TB
    subgraph clients["Product clients"]
        mac["macOS 14+<br/>SwiftUI"]
        linux["GNU/Linux<br/>JavaFX 21.0.12 + Gluon native image"]
        android["Android 12+ ARM64<br/>JavaFX 21.0.12 + Gluon"]
        tools["Command line<br/>C daemon · C TUI · C license helper"]
    end

    subgraph androidPlatform["Android platform boundary"]
        kotlin["Small Kotlin AAR<br/>VpnService · consent · files · QR<br/>secure storage · notifications · updater"]
        jni["JNI / Dalvik bridge"]
    end

    subgraph core["C17 production core"]
        control["Authenticated loopback<br/>HTTP + WebSocket"]
        config["TOML config · profiles<br/>transaction + rollback"]
        routing["Rules · rule sets · subscriptions<br/>policy groups · prompts"]
        network["SOCKS5 · HTTP(S) · TUN<br/>DNS · firewall · conditioner"]
        protocols["Protocol and chain engine<br/>WireGuard · OpenVPN · VMESS<br/>ShadowTLS · Shadowsocks · Tor"]
        developer["Capture · CA · map · rewrite<br/>breakpoints · cURL · HAR"]
        support["Traffic · events · geo<br/>license · persistence · updates"]
        lwip["lwIP packet stack"]
        crypto["OpenSSL · libsodium · libuv<br/>libcurl · llhttp"]
    end

    mac -->|HTTP + WebSocket| control
    linux -->|HTTP + WebSocket| control
    android --> kotlin --> jni --> core
    tools --> core
    control --> config
    control --> routing
    control --> developer
    routing --> network --> protocols
    protocols --> lwip
    protocols --> crypto
    core --> support
```

The Java layer is split deliberately:

- `RuntimeClient` is a typed, asynchronous view of the frozen control and event
  contracts.
- `PlatformServices` owns platform-only behavior: VPN consent and lifecycle,
  files, QR, secure storage, clipboard/browser integration, notifications,
  licensing, updates, and Android per-application routing.
- On Android, `ClambhookVpnService` owns the single C runtime. Closing the
  JavaFX activity never destroys it.
- On GNU/Linux, the JavaFX native image communicates with the supervised C
  daemon over authenticated loopback HTTP and WebSocket endpoints.

## Runtime data flow

```mermaid
sequenceDiagram
    participant User
    participant UI as JavaFX or SwiftUI client
    participant Platform as Kotlin AAR or desktop services
    participant API as C17 control/event server
    participant Router as C17 policy and chain engine
    participant Tunnel as lwIP / TUN / protocol transport
    participant Store as Traffic, events, config, license

    User->>UI: Connect or edit configuration
    UI->>Platform: Request consent / platform operation
    Platform->>API: Attach to existing runtime
    UI->>API: Typed authenticated request
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
| macOS 14+ Apple Silicon | SwiftUI | Bundled and signed C17 daemon/TUI; notarized DMG |
| GNU/Linux x86_64 and aarch64 | JavaFX 21.0.12 / GluonFX 1.0.29 | Self-contained native image beside C17 binaries; no bundled JRE |
| Android 12+ ARM64 | JavaFX 21.0.12 / GluonFX 1.0.29 | Kotlin platform AAR, JNI C runtime, signed APK and AAB; application ID `org.jpfchang.clambhook`, minSdk 31, targetSdk 36 |
| Terminal | C17 | `clambhook`, `clambhook-tui`, `clambhook-license` |

Windows development is discontinued with no planned resumption date.

## Build and test

The source build needs CMake 3.22+, Ninja, a C17 compiler, `pkg-config`,
OpenSSL 3, libsodium, libuv, and libcurl. The pinned llhttp parser is compiled
from `third_party/llhttp/`. JavaFX work uses Java 17 and Maven. Gluon
GNU/Linux native-image builds use the checksum-pinned GraalVM Community 17
toolchain provisioned by `scripts/provision-graalvm17.sh`. Android uses the
same provisioner with its `gluon` argument to select the CAP-cache-compatible,
checksum-pinned Gluon GraalVM 17 distribution.

JavaFX Maven dependencies are pinned at 21.0.12. Gluon's independently
published JavaFX 21 static ABI substrate is pinned at 21.0.1, the public bundle
available for every locked native target. GluonFX is pinned at 1.0.29. The
Linux AArch64 image is a GTK/X11 desktop build and excludes Gluon's separate
commercial DRM/framebuffer extension. Because Substrate 0.0.69 otherwise
selects its Raspberry Pi/Monocle backend for every AArch64 Linux target, the
build verifies and patches its single class-local backend selector inside an
isolated Maven repository. The AArch64 target triplet is unchanged, and
Gluon's checksum-pinned non-Monocle static SDK supplies the ordinary GTK
libraries. The desktop image includes JavaFX's software renderer as a fallback
for Xvfb, virtual machines, and systems without usable OpenGL. Monocle and DRM
archives are rejected before linking.

| Command | Purpose |
| --- | --- |
| `make build-native` | Build the production C17 daemon, TUI, and license helper. |
| `make test-native` | Run strict C tests under ASan/UBSan and the frozen license contract. |
| `make test-javafx` | Run JavaFX typed-client, model, state, and coverage tests. |
| `make test-android` | Test/lint the Kotlin AAR and build its ARM64 native payload. |
| `make build-android` | Build the shared Gluon Android application. |
| `make build-linux` | Build the host-architecture Gluon GNU/Linux native image. |
| `make build-apple` / `make test-apple` | Build and test the macOS SwiftUI client against the C runtime. |
| `make lint` | Run license/cutover checks, shell checks, warning-as-error C build, Java packaging, and Android lint. |
| `make ci-local` | Run the applicable local mirror of hosted CI. |

See [Android development](docs/android-development.md),
[release validation](docs/release-validation.md), and
[packaging](packaging/README.md).

## CI, packages, and release

```mermaid
flowchart LR
    source["Signed source commit"] --> policy["Policy gates<br/>SPDX · shell · actionlint<br/>zero retired sources"]
    policy --> ctest["C17 strict + ASan/UBSan<br/>contract and protocol fixtures"]
    policy --> jvm["JavaFX Maven tests<br/>Kotlin AAR tests"]
    policy --> apple["macOS C17 + SwiftUI<br/>build and tests"]
    ctest --> linux["GNU/Linux x86_64 + aarch64<br/>Ubuntu 24.04 · Fedora 44<br/>Gluon launch + install/uninstall"]
    jvm --> android["Android ARM64 artifacts<br/>API 31/33/36 x86_64 ATDs on Ubuntu/KVM"]
    linux --> packages["Ubuntu Debian package<br/>Fedora RPM"]
    android --> packages
    apple --> packages
    packages --> protected["Protected release workflow<br/>sign · notarize · inspect · checksum"]
    protected --> releases["GitHub Releases"]
```

Hosted distro and Android managed-device lanes are authoritative. Podman or
Docker is optional for local distro isolation. Apple's `container` CLI is not
used. Ordinary CI uploads reports only; installers are created and published
only by the protected release workflow.

Do not create a release by running build targets locally. Maintainers use
signed tags or an approved protected dispatch. See
[GitHub CI/CD](docs/github-cicd.md).

## Distribution and licensing

Official downloads are hosted at
<https://github.com/JohnThre/clambhook/releases>. macOS is distributed as a
notarized DMG for Apple Silicon Macs running macOS 14 or later.

The commercial product contract is:

- a 7-day trial for new installations; already-started month-long trials are grandfathered;
- a recurring USD 79.99 annual subscription;
- releases published during each paid term;
- versions released on or before the paid-through cutoff remain usable after cancellation or lapse;
- a maximum of 6 concurrently active devices;
- seats can be deactivated and transferred;
- cancellation stops future billing without revoking the paid term;
- resubscription can reuse the same provider-neutral license key;
- checkout uses Creem or NOWPayments, not PayPal.

```mermaid
stateDiagram-v2
    [*] --> Trial
    Trial --> Active: verified annual payment
    Active --> Active: annual renewal
    Active --> Deactivated: deactivate seat
    Deactivated --> Active: activate on another device
    Active --> Fallback: cancel or paid term lapses
    Fallback --> Active: resubscribe with the same key

    note right of Active
        C17 license helper evaluates the signed snapshot
        JavaFX, SwiftUI, and Kotlin platform services share the result
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

The first-party application is GPL-3.0-only, with separate written commercial
terms available. `clib/**` is Apache-2.0. Pinned third-party material retains
its upstream licenses and provenance. See [licensing](LICENSING.md),
[notice](NOTICE), and [third-party notices](THIRD_PARTY_NOTICES.md).

## Security and contribution

Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).
Contributions require the agreement in [CLA.md](CLA.md), SPDX headers, tests,
and preservation of the published control, persistence, identifier, and
licensing contracts.

## Author

Pengfan Chang — <support@swiphtgroup.com>
