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

- `clambhook_core`: a C17 ABI with a serialized libuv runtime thread, JSON
  parser, bounded event replay ring, SOCKS address codec, and compiled routing
  rule matcher;
- `clambhook_crypto`: the existing C crypto surface, now covering AES-128-GCM,
  AES-256-GCM, ChaCha20-Poly1305, SHA-224, and random bytes through C
  dependencies;
- `clambhook-license-c`: the complete helper command surface, including local
  evaluation/status/recovery and libcurl activation/device actions;
- `clambhook-c`: an explicitly guarded migration daemon with a small libuv +
  llhttp API slice and hardened bearer/Host/Origin checks;
- `clambhook-linux-c`: an additive `GtkApplication` dashboard using GTK 4,
  libsoup 3, and json-glib;
- `clambhook_jni`: the thin JNI ownership/callback boundary used by the Kotlin
  bridge during Android cutover;
- sanitizer-backed native unit tests and a byte-for-byte license parity gate
  against the current helper.

`clambhook-c` requires `--allow-incomplete-native`. This deliberate guard keeps
it out of production packages until configuration, listeners, every protocol,
the full control API/WebSocket surface, persistence, and platform lifecycle
tests pass their parity gates.

## Build and test

The native host build requires CMake, Ninja, pkg-config, libuv, libsodium,
OpenSSL, libcurl, and llhttp. The optional Linux client additionally requires
GTK 4, libsoup 3, and json-glib.

```sh
make build-native
make test-native
make build-linux-gtk
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

The production runtime stays on the gomobile bridge until the C runtime can be
built for every packaged ABI and the same runtime/API/VPN tests pass through
`NativeClambhookBridge`. The JNI class is additive and never loads its library
unless selected, so Android 11 users retain the proven runtime throughout the
migration.

## Cutover gates

Cutover is allowed only after all of the following are green:

1. C unit tests pass under AddressSanitizer and UndefinedBehaviorSanitizer.
2. Differential fixtures match the legacy implementation for config, rules,
   API JSON/status codes, events, license behavior, and each protocol wire
   transcript.
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
