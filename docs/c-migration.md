# C runtime and native client migration

Clambhook is moving from its Go runtime to a C17 runtime in gated phases. The
target platform architecture is:

| Surface | Target implementation |
| --- | --- |
| Daemon, listeners, routing, protocols, persistence, API, TUI | C17 |
| GNU/Linux desktop GUI | C17 with GTK 4 |
| Android GUI | Kotlin with Jetpack Compose, calling C through JNI |
| macOS GUI | SwiftUI, calling the same C runtime boundary |

The legacy Go daemon, Go TUI, and Kotlin/Compose Desktop Linux client remain
shipping implementations while their parity work is in progress. The gomobile
Android bridge remains a source-level behavior oracle but is no longer packaged
or selected by the Android app. Remaining legacy implementations must not be
removed merely because an additive C target compiles.

## Frozen compatibility contracts

The migration preserves these external contracts:

- command names, flags, exit behavior, and version output;
- TOML configuration fields, defaults, validation, and relative-path rules;
- HTTP paths, methods, authorization, Host/Origin protection, status codes,
  request limits, and JSON shapes;
- WebSocket event envelopes, replay ordering, and slow-consumer behavior;
- persisted traffic, rule-cache, subscription, license, and settings data;
- protocol wire formats and cryptographic behavior;
- Android application ID, Kotlin/Compose UI behavior, VPN lifecycle, and an
  Android 12 (API 31) minimum runtime.

Any intentional contract change needs a separately documented migration. A C
implementation that is merely similar is not sufficient for cutover.

## Current native slice

The additive CMake build currently provides:

- `clambhook_core`: a C17 ABI with a serialized libuv runtime thread, a pinned
  TOML 1.0 parser, configuration validation and relative-path handling,
  atomic writes with bounded backups, debounced validated reloads, JSON
  parsing/encoding, a bounded event replay ring, SOCKS address codec, compiled
  routing rule matcher, and native SOCKS5/HTTP proxy listeners with bounded
  handshakes, authentication, rule blocking, connection ceilings, and
  deterministic relay shutdown. Its native chain dialer supports direct TCP,
  Trojan/clambback, Shadowsocks AEAD-2018, Tor SOCKS5, and VMESS-AEAD TCP
  streams, including nested encrypted hops. A protocol-neutral packet API now
  supports direct UDP, Shadowsocks AEAD-2018 UDP, Trojan/clambback UDP, and
  VMESS-AEAD UDP. Stream-carrier chains may end in Trojan/clambback or VMESS
  packet mode, and the SOCKS5 listener implements `UDP ASSOCIATE` with
  asynchronous route-keyed sessions. Process matchers resolve local TCP and
  UDP socket ownership through native `libproc` on Darwin and `/proc` on Linux,
  with macOS code-signing enrichment. A bounded native network watcher reports
  interface/SSID state and applies first-match profile triggers through the
  serialized runtime. The encrypted-DNS library validates DNS transaction IDs
  and question sections, returns SERVFAIL after upstream failover, expands
  Control D resolver shorthand without system-DNS bootstrap loops, and performs
  route-planned DoH and DoT exchanges with TLS 1.2 or newer. A pinned lwIP
  2.2.1 `NO_SYS` raw core supplies the portable IPv4/IPv6 packet-stack
  foundation used by host runtimes and the Android JNI library;
- `clambhook_crypto`: the existing C crypto surface, now covering AES-128-GCM,
  AES-256-GCM, ChaCha20-Poly1305, SHA-224, and random bytes through C
  dependencies;
- `clambhook-license-c`: the complete helper command surface, including local
  evaluation/status/recovery and libcurl activation/device actions;
- `clambhook-c`: an explicitly guarded migration daemon that loads and watches
  native TOML configuration, honors the active profile's API address, and
  serves status/profile/server/rule/policy-group/rule-set JSON plus native
  route explanations through a small libuv + llhttp API with hardened
  bearer/Host/Origin checks. Profile-scoped reads decode URL query parameters,
  return 404 for unknown profiles, and share one requested-profile contract
  with JNI. `PUT /api/v1/profiles/active` now validates the requested profile
  against a fresh disk snapshot, atomically replaces the top-level TOML
  selection with a retained backup, reloads the live runtime transactionally,
  and restores the previous document if the selected profile cannot start. It
  returns `persisted:true` plus `backup_path` for file-backed daemon configs and
  `persisted:false` for an in-memory runtime. `POST /api/v1/routes/explain`
  shares the native route explanation path. Config export/import also matches
  the 4 MiB symmetric transfer contract: export reads a fresh validated disk
  snapshot and supplies attachment metadata, while import validates before
  writing, retains the old document as a backup, reloads live, restores disk on
  apply failure, and reports the resulting profiles and backup path.
  Config-derived reads also cover `/api/v1/dns`,
  `/api/v1/config/settings`, `/api/v1/conditioner`, and
  `/api/v1/rule-subscriptions`, including requested-profile 404 behavior and
  normalized empty/default fields. `PUT` support for the DNS, settings, and
  conditioner endpoints now uses one typed JSON-tree mutation path and a
  complete semantic TOML renderer. File-backed mutations retain an atomic
  backup, validate the rendered document before writing, apply it to the live
  runtime transactionally, and restore the previous file and runtime when a
  new listener cannot start. The writer preserves the live active profile
  selected by a network trigger rather than reverting to a stale disk
  selection. The same transaction now implements `PUT /api/v1/rules`,
  append-only `POST /api/v1/rules`, and `PUT` replacement for policy groups,
  rule sets, and rule subscriptions with ordered persistence and the existing
  profile/collection/backup response contract. Manual `select` group changes
  now validate group type and membership, persist transactionally, and return
  the nested Go-compatible policy snapshot. Config-derived policy reads include
  normalized defaults, selected chain, selection mode/reason, and live probe
  results. A C-owned policy manager runs bounded HTTP/HTTPS HEAD probes through
  every configured chain in parallel, verifies TLS by default, and applies
  `url-test`, `fallback`, stable-hash `load-balance`, sticky `smart`, and manual
  `select` routing for TCP and eligible UDP members. The Go-compatible
  `POST /api/v1/policy-groups/test` endpoint validates its optional group name,
  refreshes either one group or every group in the running native engine, and
  returns the updated policy snapshot. The manager follows runtime start, stop,
  reload, and profile-switch transactions on both the host daemon and Android
  JNI runtime. Remote rule sets and subscriptions now share a portable
  C17 parser/cache subsystem for
  `auto`, `plain`, `hosts`, and `adblock` input, normalized sorted domain/CIDR
  matchers, the 5 MiB/200,000-entry contract, and version-1 Go cache
  compatibility. The native refresh endpoints use conditional
  ETag/Last-Modified requests, reject non-public and metadata destinations,
  pin validated DNS results, permit only same-origin redirects, atomically
  replace cache files, retain old caches on failure, rebuild live rules, and
  expose cache counts, generated rule names, timestamps, skipped entries, and
  per-feed errors. Rule cleanup/from-connection creation and DNS upstream route
  annotations are still gated.
  `/api/v1/developer/settings` now has config-derived reads and transactional
  writes with bounded defaults, lowercase/trimmed redaction and TLS-host lists,
  the same first-enable HTTPS-capture acknowledgement requirement, invalid
  no-write behavior, and CA-path omission. This is configuration parity only;
  map, breakpoint, and rewrite rule collections now support config-derived
  reads, ordered replacement, and percent-decoded ID deletion with the
  Go-compatible developer/backup response. The API maps wire `ops` to TOML
  `op` without weakening validation. Opt-in plain-HTTP inspection now captures
  bounded request and response bodies directly in the C proxy, applies the
  configured header and query-parameter redaction lists, and exposes native
  status, filtered entry lists, entry detail, cURL export, HAR 1.2 export, and
  clear operations. A bounded, non-executing C cURL importer is shared by the
  daemon API and Android JNI runtime. Fragmented response headers and
  concurrent capture/API access have native integration coverage. HTTPS MITM,
  CA management, replay, standalone send, breakpoint, map, and rewrite
  execution remain guarded.
  Configured proxy listeners participate in runtime start, stop, reload,
  profile switching, and status reporting;
- `clambhook-tui-c`: a native terminal client with bounded libcurl requests,
  bearer-token support, raw-terminal restoration, offline retry, status,
  listener, profile, traffic, and prompt views, live profile switching,
  connect/disconnect controls, and once/session/permanent prompt decisions. In
  non-interactive use it prints one plain-text snapshot and exits, which also
  provides a scriptable API smoke check;
- `clambhook-linux-c`: an additive `GtkApplication` dashboard using GTK 4,
  libsoup 3, and json-glib. Its asynchronous native-API client now renders
  status/listeners, profile selection, traffic, servers, policy groups,
  prompts, encrypted DNS, developer capture state/entries, and network
  conditioning. It provides connect/disconnect, refresh, active-profile
  switching, policy latency tests/manual chain selection, all five pending-
  prompt decisions with optional host/port/protocol matching, capture
  enable/disable, and confirmation-gated capture clearing. Mutation payloads
  and identifier escaping are covered by display-independent GLib model
  fixtures and a `--version` executable smoke run in the Trisquel/Rocky/Alma
  lanes. Capture text/method/error filters, request/response detail previews,
  and clipboard cURL export use only native C routes and have model fixtures.
  Encrypted-DNS and conditioner editors construct typed, display-independent
  JSON payloads before the daemon validates, persists, and applies them
  transactionally. A reconnecting libsoup WebSocket subscriber listens to the
  native `connection.*` and `rule.*` feed and coalesces bursty updates into
  bounded traffic/status refreshes. Silent Mode review rows promote the
  daemon-recorded action to session, until-quit, or persisted rules without
  letting the UI alter the recorded allow/deny result. The non-executing cURL
  importer exposes only a parsed preview and rejects file reads; HAR 1.2 export
  writes the bounded redacted archive through a native save dialog. Remaining
  configuration/rule editors, capture repeat/composition, accessibility QA,
  license actions, and production packaging remain open;
- `clambhook_jni`: the thin JNI ownership/callback boundary used by the Kotlin
  bridge during Android cutover. Gradle now builds and packages it with the NDK
  for arm64-v8a, armeabi-v7a, x86, and x86_64. The JNI runtime now accepts raw
  IPv4/IPv6 packets in C and returns stack output through the Kotlin packet
  writer callback;
- sanitizer-backed native unit tests and a byte-for-byte license parity gate
  against the current helper.

`clambhook-c` requires `--allow-incomplete-native`. This deliberate guard keeps
it out of production packages until configuration, listeners, every protocol,
the full control API/WebSocket surface, persistence, and platform lifecycle
tests pass their parity gates.

The listener data plane currently completes direct-rule routes, single-hop test
chains whose protocol is `direct`, and TCP chains containing `trojan`,
wire-compatible `clambback`, `shadowsocks`, `shadowtls`, `tor`, and `vmess`
hops. The C Trojan implementation
matches the legacy SHA-224 password header and SOCKS address encoding, requires
TLS 1.2 or newer, supports SNI and ALPN, verifies certificates by default, and
honors the existing `skip_cert_verify` escape hatch. The C Shadowsocks
implementation supports the same AEAD-2018 methods (`aes-128-gcm`,
`aes-256-gcm`, and `chacha20-ietf-poly1305`), MD5 `EVP_BytesToKey`,
HKDF-SHA1 `ss-subkey` derivation, per-direction salts, authenticated chunk
lengths, and little-endian nonce counters. Legacy stream ciphers fail closed.
Its UDP codec uses a fresh CSPRNG salt and zero nonce per packet, authenticates
the SOCKS destination together with the payload, and rejects oversized or
tampered datagrams. Local integration tests cover every Shadowsocks TCP and UDP
method, direct UDP, plus nested
Trojan/clambback and nested Shadowsocks streams under ASan/UBSan. The Tor row
matches the legacy TCP-only SOCKS5 design, preserves remote domain and `.onion`
resolution, supports optional RFC 1929 stream-isolation credentials, and is
tested both directly and after a Trojan hop.

The native VMESS row supports the modern AEAD header (`alter_id = 0`) over raw
TCP or TLS, AES-128-GCM and ChaCha20-Poly1305 body streams, certificate/SNI/ALPN
settings through the shared TLS transport, remote target encoding, authenticated
responses, and fail-closed 16-bit nonce exhaustion. UDP command mode preserves
datagram boundaries over a portable Unix datagram socketpair and fixes the
remote target at session setup, matching the legacy VMESS packet contract.
Fixed Go-derived KDF/address fixtures and local fake-server TCP, UDP, and nested
carrier transcripts validate both cipher methods under ASan/UBSan. Legacy VMESS
authentication and non-TCP outer transports remain out of scope.

Trojan and wire-compatible clambback packet mode send the UDP-associate command
inside the existing TLS handshake and parse their address + length + CRLF
stream frames incrementally across partial reads. The same packet endpoint can
be placed after native direct, encrypted, Tor, VMESS, or ShadowTLS carrier hops;
local single-hop and nested Trojan-to-clambback transcripts are green.

The native ShadowTLS row implements strict v3 as a non-final carrier hop. It
uses a CSPRNG-seeded deterministic replay stream inside a private OpenSSL 3
library context to place the required HMAC-SHA1 signature in the TLS 1.3
ClientHello session id without modifying process-global randomness. It then
authenticates and restores the server's rewritten handshake records before
switching to chained four-byte-HMAC data frames. Certificate verification is
enabled by default; SNI, ALPN, and the existing explicit verification escape
hatch are preserved. A real TLS 1.3 relay test validates the complete handshake
and echo path under ASan/UBSan, and final-hop ShadowTLS configurations fail
closed because an addressed inner protocol is required.

The SOCKS5 UDP relay binds to the control connection's local interface, pins
the first client endpoint (and any explicitly requested port), rejects
fragmentation, re-evaluates routing for each datagram, reuses one asynchronous
packet session per selected route, and ties teardown to the TCP control
connection. WireGuard, OpenVPN, DoQ, traffic persistence, and developer
inspection execution remain cutover blockers;
datagram protocols that cannot
be represented by the native stream-carrier model fail closed.

Native encrypted DNS currently implements DoH with libcurl HTTP/2 negotiation
and DoT with length-prefixed OpenSSL streams. Both transports use the route
planner's already-connected descriptor, so direct hostname endpoints require
validated bootstrap IPs while chained routes preserve remote name resolution.
Each exchange rejects mismatched transaction IDs or question sections, and
total upstream failure still produces an owned SERVFAIL response for the packet
stack. Control D custom and free resolver forms derive the same endpoints,
TLS names, and anycast bootstrap addresses as the legacy implementation. DoQ is
explicitly rejected until a portable QUIC dependency and transcript parity are
available; this is not a silent downgrade to DoH or DoT. Runtime start, reload,
profile switching, and rollback now construct the DNS proxy transactionally;
its stream dials use the same compiled rules, policy-group selection, default
chain, and direct bootstrap addresses as the proxy listeners. Runtime status
reports whether encrypted DNS is active and names its configured upstreams.

The native TUN foundation vendors the unmodified BSD-3-Clause lwIP 2.2.1
release and presents it as a layer-3 IPv4/IPv6 interface. Runtime start,
reload, profile selection, rollback, stop, and periodic timeout processing own
the stack on the same serialized thread as other native services. The active
profile's MTU and first IPv4/IPv6 CIDRs are applied, status reports `tun` only
while the stack is active, and raw packet injection is rejected unless that
profile enabled TUN. Host tests verify IPv4 and IPv6 ICMP echo checksums,
configured CIDRs, singleton ownership, profile teardown/recreation, and output
callbacks. Android builds the identical core for all four ABIs and its JNI
runtime exercises the same injection/callback boundary. This is not yet the
TUN cutover: IPv4 and IPv6 TCP use bounded transparent flow mappings, preserve
numeric dial targets, route using recovered domain hints when available, bridge
native direct or encrypted descriptors with nonblocking backpressure, and
restore reply tuples and checksums. IPv4 and IPv6 UDP use bounded, idle-expiring
five-tuple sessions over the native direct, Shadowsocks, Trojan/clambback, or
VMESS packet transports. UDP/53 is intercepted by the route-planned encrypted
DNS proxy; validated A and AAAA answers populate a TTL-capped, bounded cache so
subsequent domain rules still apply while transport dialing remains numeric.
Both UDP families validate lengths and checksums and reject over-MTU output.
A bounded pre-routing cache reassembles out-of-order IPv4 and IPv6 fragments,
rejects overlaps and inconsistent totals, expires incomplete flows, and removes
the IPv6 Fragment header before transport translation. Common IPv6 hop-by-hop,
routing, destination-options, and authentication extension chains are parsed
with depth and size limits. Incomplete TCP handshakes now expire after thirty
seconds and abort their private lwIP PCB before releasing the mapping. Android
now resolves rule-enforced direct, named-chain, and policy-group TCP and UDP
routes through that shared protocol layer. A C-owned ten-millisecond timer
receives delayed remote packets without Kotlin polling, active-profile changes
transactionally rebuild the compiled rules and packet stack with rollback, and
an API 31 device test verifies an actual loopback UDP request/reply. The Android
NDK build pins and checksums the official OpenSSL 3.5.8 LTS source, statically
links it across all four ABIs at API 31, and packages the upstream Apache 2.0
license without relicensing Clambhook. The physical Pixel suite executes all
three native AEAD families through JNI, but physical hardware is optional and
the managed API 31/33/36 matrix is authoritative. The production VPN factory
selects this C/JNI runtime and packages no gomobile AAR. Android now intercepts
UDP/53 through the shared DoT engine and emits SERVFAIL on encrypted-upstream
failure. DoH fails closed on Android until the NDK build links libcurl; it does
not fall back to plaintext DNS. Full VPN-service lifecycle tests remain open.

Native process attribution is best-effort at the operating-system boundary,
matching the rollback contract when permissions hide another process. It
normalizes executable name and path for `processes` rules and records signing
identity/status on macOS without invoking a shell. TCP and UDP lookup fixtures
cover live sockets, and an end-to-end SOCKS5 test proves that the owning native
test process selects a process-name reject rule. The listener avoids ownership
enumeration entirely when the compiled rule set has no process matcher.

The native network watcher probes immediately and then every ten seconds,
emitting only state changes. On macOS it invokes absolute-path `scutil`,
`networksetup`, and `ipconfig` commands with conservative interface-name
validation, close-on-exec descriptors, and one five-second deadline for the
whole probe. It preserves interface-only triggers when macOS 14+ withholds the
SSID without Location authorization. GNU/Linux reads `/proc/net/wireless` and
falls back to the first up, non-loopback interface. Trigger matching trims and
compares SSID/interface values case-insensitively, ignores an all-empty trigger,
and selects only the first matching profile in configuration order. Listener
rebuild failure rolls back to the prior profile; native status exposes
`interface_name`, `ssid`, and `is_wifi`. Event-stream publication for the
automatic switch remains part of the full control-API gate.

## Build and test

The native host build requires CMake, Ninja, pkg-config, libuv, libsodium,
OpenSSL 3.0 or newer, libcurl, and llhttp. The optional Linux client
additionally requires GTK 4, libsoup 3, and json-glib. `make build-linux-gtk`
also runs the display-independent model and executable tests.

```sh
make build-native
make test-native
make build-linux-gtk
make build-android-native
```

For a direct all-options developer build:

```sh
cmake -S . -B build-native -G Ninja \
  -DCLAMBHOOK_ENABLE_SANITIZERS=ON \
  -DCLAMBHOOK_BUILD_GTK=ON \
  -DCLAMBHOOK_BUILD_JNI=ON
cmake --build build-native
ctest --test-dir build-native --output-on-failure
cmake --build build-native --target native-license-parity
```

## Android 12+ gate

The Android app remains Kotlin/Jetpack Compose with `minSdk = 31`,
`targetSdk = 36`, and `compileSdk = 36`. Gradle build-managed AOSP devices
exercise Compose instrumentation tests at API 31, 33, and 36:

```sh
make test-android-compatibility
```

Every Android build now compiles and packages `libclambhook_jni.so`; the
production VPN factory selects the native C/JNI packet runtime and no gomobile
AAR is present in the application. Focused managed-device
tests load TOML in C and exercise start, stop, status, profiles, server/rule
payload decoding, profile switching, compiled-rule route explanations, a raw
IPv4 packet round trip, delayed direct UDP, and encrypted-DNS DoT interception
with fail-closed DoH. The native route callbacks now
dial configured encrypted TCP/UDP chains, and the JNI suite calls the statically
linked OpenSSL implementation for AES-128-GCM, AES-256-GCM, and
ChaCha20-Poly1305. The complete managed-device run is required on the API 31
floor. A physical Pixel 3a XL running Android 12/API 32 has also passed focused
instrumentation, but connected hardware is optional and never substitutes for
the API 31/33/36 CI matrix.

## Cutover gates

Cutover is allowed only after all of the following are green:

1. C unit tests pass under AddressSanitizer and UndefinedBehaviorSanitizer.
2. Differential fixtures match the legacy implementation for config, rules,
   API JSON/status codes, events, license behavior, and each protocol wire
   transcript. Trojan/clambback TCP/UDP, Shadowsocks TCP/UDP, direct UDP, Tor
   SOCKS5/nested-stream, VMESS-AEAD TCP/UDP, nested packet carriers, and
   ShadowTLS v3 fixtures are green. DNS wire, failover/SERVFAIL, Control D,
   bootstrap-loop, DoH, and DoT fixtures are green; DoQ, WireGuard, and OpenVPN
   rows are still open.
3. Listener and protocol integration tests cover TCP, direct/Shadowsocks UDP,
   SOCKS5 UDP association reuse and cancellation, reload rollback, concurrency
   limits, native macOS/Linux process-rule attribution, network-triggered
   first-match profile switching, remaining protocol UDP rows, and platform
   TUN lifecycle. Native IPv4/IPv6 TCP mapping and descriptor
   bridging, IPv4/IPv6 UDP session forwarding, encrypted-DNS interception,
   TTL-bounded domain recovery, out-of-order fragment reassembly and overlap
   rejection, common IPv6 extension parsing, ICMP injection, checksum
   rewriting, direct route dialing, Android direct/encrypted route linkage,
   OpenSSL AEAD execution, UDP timer/profile switching, and runtime lifecycle
   fixtures are green. Android encrypted DNS and production VPN lifecycle rows
   remain open.
4. GTK functional/accessibility QA matches the current Linux feature set.
5. Android unit, lint, build, Compose instrumentation, VPN, background, and
   process-restart tests pass on API 31, 33, and 36.
6. macOS tests and packaging smoke pass against the C daemon/runtime.
7. `.deb`, `.rpm`, Android, and macOS packaging use no Go-built artifact.
8. A repository search finds no production Go source, `go.mod`, gomobile/cgo
   step, Go binary, or Go toolchain requirement.

Only after gate 7 should production target names lose the temporary `-c`
suffix. Only after gate 8 should the legacy implementation be deleted.
