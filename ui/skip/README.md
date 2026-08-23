# ClambHook shared Skip UI (archived experiment)

> **Status: archived — not wired into any shipping client.** This package is
> a proof-of-concept for a Swift-first shared UI. The selected Android
> architecture is native Kotlin/Jetpack Compose over the C runtime's JNI
> boundary, so this package is not an Android migration target. It is **not** built, tested, or
> imported by the Apple, Android, TUI, or Linux clients in their release
> pipelines, and it is excluded from `make test`. Its integration is blocked by a
> Skip 1.9.4 standalone-library resolution defect (see "Known blocker" below), so
> `swift build`/`swift test` do not run against it standalone. Treat everything
> here as a design spike until that blocker clears and the module is consumed
> inside a `skip init`-generated app workspace. The shared-model logic
> (`TunnelStatus`, `formatByteRate`) has behavioral tests in
> `Tests/ClambhookUITests` that run in that workspace.

`ui/skip` explored transpiling SwiftUI models/views into Kotlin/Jetpack Compose
with Skip Lite (`skipstone`). The shipping Android source of truth remains the
Kotlin code under `ui/android`.

## Current module

- Package: `ClambhookSkipUI`
- Product: `ClambhookUI`
- Source module: `Sources/ClambhookUI`
- First shared surface:
  - `TunnelStatus` — platform-neutral status state over `TunnelRuntime` / Apple shared app model data.
  - `TunnelStatusView` — Surge-for-iOS-style connection card with status, profile, throughput, active connections, and connect/disconnect action.

## Toolchain

Install and validate Skip:

```sh
brew install skiptools/skip/skip
ANDROID_HOME="$HOME/Library/Android/sdk" \
JAVA_HOME="$HOME/Library/Java/JavaVirtualMachines/jbr-17.0.14/Contents/Home" \
skip checkup
```

`skip checkup` currently passes toolchain/build prerequisites on this machine; its generated Kotlin test harness reports a test-name extraction issue in Skip 1.9.4. A fresh `skip init --transpiled-app` app exports successfully, proving the installed toolchain can produce Android artifacts.

## Build / transpile

From `ui/skip`:

```sh
ANDROID_HOME="$HOME/Library/Android/sdk" \
JAVA_HOME="$HOME/Library/Java/JavaVirtualMachines/jbr-17.0.14/Contents/Home" \
skip verify

swift build
```

The `swift build` runs Skip's `skipstone` plugin and emits generated Kotlin under `.build/plugins/outputs/.../skipstone/`.

## Superseded integration sketch

The following records the original experiment and is not the active product
plan. See [`../../docs/c-migration.md`](../../docs/c-migration.md) for the
selected C/GTK/JNI architecture.

1. Keep business/runtime state in the existing Android `LocalTunnelApi` and Apple `AppleAppModel`/SharedApp layers.
2. Map platform state into `TunnelStatus`:
   - `running` ← Android `DashboardState.status.running`; Apple `AppleAppModel` equivalent.
   - `profileName` ← active profile.
   - `activeConnections` ← traffic summary active connection count.
   - `downloadBytesPerSecond` / `uploadBytesPerSecond` ← traffic summary rates.
3. Android: import generated `ClambhookUI` Compose output and replace the Kotlin `StatusCard` connection header with the transpiled `TunnelStatusView`.
4. Apple: import `ClambhookUI` package into the Xcode project and reuse `TunnelStatusView` in the macOS/iOS status surface.

## Known blocker (verified 2026-07-15)

`skip export` / `swift build` on this standalone Skip **library** fails during
dependency resolution: SwiftPM never materializes the `skip.git` package (which
vends the `skipstone` build-tool plugin and the `SkipTest` product), so the
build errors with `product 'skipstone'/'SkipTest' ... not found in package 'skip'`
and `.build/checkouts/skip` is never created.

Reproduced identically across every combination tried, so this is an
environment/Skip-1.9.4 behavior for standalone library packages, not a package
error:

- Swift 6.3.3 (Xcode 26.6) and Swift 6.1.3 (swiftly)
- Fresh `swift package resolve`, `swift build`, `swift build --build-tests`, `skip export`
- SwiftPM cache purge; manual seeding of a known-good `.build/checkouts/skip`; a local `set-mirror` for `skip.git`
- `Package.swift` matched byte-for-byte (modulo names) to a `skip init` app that builds

SwiftPM prunes the plugin-only `skip` package from a library build graph
("Removing https://source.skip.tools/skip.git") because only the (excluded)
test target references a `skip` product.

Proven-working path: a `skip init --transpiled-app` app **does** transpile and
`skip export` produces real `*-debug.aar` (Compose), `.apk`, `.aab`, and iOS
`.ipa`. The toolchain transpiles SwiftUI to Compose correctly; only standalone
library resolution is affected.

Remediation options:
1. Consume this module inside a `skip init`-generated app workspace (the proven
   path) instead of exporting it standalone.
2. Track the standalone-library resolution issue on skip.dev/forums; revisit
   after a Skip release certified for the installed Swift toolchain.

Note: `swiftly` installed Swift 6.1.3 during investigation and set it as the
swiftly global default; the machine's Xcode `swift` is unaffected.
