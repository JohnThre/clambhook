# C runtime and native client migration

Clambhook is moving from its Go runtime to a C17 runtime in gated phases. The
target platform architecture is:

| Surface | Target implementation |
| --- | --- |
| Daemon, listeners, routing, protocols, persistence, API, TUI | C17 |
| GNU/Linux desktop GUI | C17 with GTK 4 |
| Android GUI | Kotlin with Jetpack Compose, calling C through JNI |
| macOS GUI | SwiftUI, calling the same C runtime boundary |

The legacy Go daemon, Go TUI, gomobile Android bridge, and Kotlin/Compose
Desktop Linux client remain the shipping implementations while parity work is
in progress. They are the behavior oracles and rollback path; they must not be
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
  Android 11 (API 30) minimum runtime.

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
  deterministic relay shutdown. Its native chain dialer supports direct TCP
  plus Trojan and clambback TCP streams, including nested encrypted hops;
- `clambhook_crypto`: the existing C crypto surface, now covering AES-128-GCM,
  AES-256-GCM, ChaCha20-Poly1305, SHA-224, and random bytes through C
  dependencies;
- `clambhook-license-c`: the complete helper command surface, including local
  evaluation/status/recovery and libcurl activation/device actions;
- `clambhook-c`: an explicitly guarded migration daemon that loads and watches
  native TOML configuration, honors the active profile's API address, and
  serves status/profile/server/rule/policy-group/rule-set JSON plus native
  route explanations through a small libuv + llhttp API with hardened
  bearer/Host/Origin checks. Configured proxy listeners participate in runtime
  start, stop, reload, profile switching, and status reporting;
- `clambhook-linux-c`: an additive `GtkApplication` dashboard using GTK 4,
  libsoup 3, and json-glib;
- `clambhook_jni`: the thin JNI ownership/callback boundary used by the Kotlin
  bridge during Android cutover. Gradle now builds and packages it with the NDK
  for arm64-v8a, armeabi-v7a, x86, and x86_64;
- sanitizer-backed native unit tests and a byte-for-byte license parity gate
  against the current helper.

`clambhook-c` requires `--allow-incomplete-native`. This deliberate guard keeps
it out of production packages until configuration, listeners, every protocol,
the full control API/WebSocket surface, persistence, and platform lifecycle
tests pass their parity gates.

The listener data plane currently completes direct-rule routes, single-hop test
chains whose protocol is `direct`, and TCP chains made entirely of `trojan`
and/or wire-compatible `clambback` hops. The C implementation matches the
legacy SHA-224 password header and SOCKS address encoding, requires TLS 1.2 or
newer, supports SNI and ALPN, verifies certificates by default, and honors the
existing `skip_cert_verify` escape hatch. Local TLS integration tests cover a
single hop and a nested Trojan-to-clambback stream under ASan/UBSan.

Shadowsocks, VMESS, ShadowTLS, WireGuard, OpenVPN, Tor, UDP ASSOCIATE, TUN,
DNS, prompts, traffic persistence, and developer inspection remain cutover
blockers; unsupported chain protocols fail closed.

## Build and test

The native host build requires CMake, Ninja, pkg-config, libuv, libsodium,
OpenSSL, libcurl, and llhttp. The optional Linux client additionally requires
GTK 4, libsoup 3, and json-glib.

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

## Android 11+ gate

The Android app remains Kotlin/Jetpack Compose with `minSdk = 30`,
`targetSdk = 36`, and `compileSdk = 36`. Gradle build-managed AOSP devices
exercise Compose instrumentation tests at API 30, 33, and 36:

```sh
make test-android-compatibility
```

Every Android build now compiles and packages `libclambhook_jni.so`; the
production tunnel factory still selects the gomobile rollback runtime until
the remaining runtime/API/VPN operations pass parity. A focused managed-device
test loads TOML in C and exercises start, stop, status, profiles, server/rule
payload decoding, profile switching, and compiled-rule route explanations over
JNI; both focused cases pass on the API 30 floor. API 33/36 remain mandatory
before cutover.

## Cutover gates

Cutover is allowed only after all of the following are green:

1. C unit tests pass under AddressSanitizer and UndefinedBehaviorSanitizer.
2. Differential fixtures match the legacy implementation for config, rules,
   API JSON/status codes, events, license behavior, and each protocol wire
   transcript. Trojan/clambback TCP header and nested-stream fixtures are green;
   the remaining protocol rows are still open.
3. Listener and protocol integration tests cover TCP, UDP, cancellation,
   reload rollback, concurrency limits, and TUN lifecycle.
4. GTK functional/accessibility QA matches the current Linux feature set.
5. Android unit, lint, build, Compose instrumentation, VPN, background, and
   process-restart tests pass on API 30, 33, and 36.
6. macOS tests and packaging smoke pass against the C daemon/runtime.
7. `.deb`, `.rpm`, Android, and macOS packaging use no Go-built artifact.
8. A repository search finds no production Go source, `go.mod`, gomobile/cgo
   step, Go binary, or Go toolchain requirement.

Only after gate 7 should production target names lose the temporary `-c`
suffix. Only after gate 8 should the legacy implementation be deleted.
