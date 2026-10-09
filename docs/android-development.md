<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Android development

ClambHook supports Android 12/API 31 through API 36 on ARM64. The application
ID is `org.jpfchang.clambhook`, `minSdk` is 31, and `targetSdk` is 36. API 30
and 32-bit release ABIs are not supported.

## Ownership boundaries

The user-facing application is the shared Kotlin / Compose Multiplatform UI
(`ui/kotlin/shared`), hosted by the Android application module
`ui/kotlin/app` (`MainActivity`). The Android library `ui/kotlin/platform`
(published as `clambhook-android-platform`) has no product screens.

The platform library owns:

- VPN consent and `VpnService` foreground lifecycle;
- the single JNI-backed C17 runtime and TUN descriptor;
- process restart, revoke, always-on, and reconnect behavior;
- per-application allow/bypass routing;
- file import/export, QR scan/share, and FileProvider updates;
- encrypted secure storage, clipboard/browser, notifications, licensing, and
  updater integration.

The Compose activity reaches the runtime through `AndroidBackend` and
`AndroidPlatformServices` (`shared/src/androidMain`), which call
`AndroidRuntimeFacade` directly. Activity destruction closes only the view
attachment; it does not stop or destroy the service-owned runtime.

## Toolchain

- Java 17
- Android Gradle Plugin 9.3.2 with built-in Kotlin and the Kotlin 2.4.10
  serialization plugin
- Gradle 9.7.1 wrapper
- compileSdk 36 with build-tools 36.0.0; targetSdk 36 (stable SDK only;
  dependencies that need compileSdk 37 are pinned below that line)
- Android NDK `28.2.13676358`
- CMake 3.22.1
- Compose Multiplatform 1.11.1 and AndroidX Activity Compose 1.13.0
- Bouncy Castle `bcpg-jdk18on` 1.86 for updater signature verification
- OpenSSL 3.5.8 and curl 8.18.0 source archives verified by SHA-256

Use the Android CLI for local SDK and device management:

```sh
android info
android sdk list --all 'platforms*'
android sdk install platforms/android-36 build-tools/36.0.0
```

The Gradle build will provision the pinned native sources into
`ui/kotlin/.native-deps`. Release packages contain only `arm64-v8a` native
libraries.

## Build and test

```sh
make test-linux      # shared Kotlin/Compose UI tests (JVM)
make test-android
make build-android
```

`make test-android` runs Kotlin unit tests, Android lint for both modules,
ARM64 JNI/C compilation, and release AAR and APK assembly. `make build-android`
builds the release APK and App Bundle. They are unsigned unless the
`CLAMBHOOK_ANDROID_KEYSTORE_*` environment is present.

Local installation and launch use the Android CLI:

```sh
android emulator list
android emulator start clambhook-api36
android run --device emulator-5554 --apks /path/to/ClambHook-arm64.apk
android layout --device emulator-5554 --pretty
```

Use `android layout --diff` after an interaction to inspect only changed UI
nodes. Use `android screen capture` only when the hierarchy cannot represent
the visual state.

## Managed-device matrix

Hosted CI is authoritative and runs `aosp_atd/x86_64` images on Ubuntu 24.04
x86_64 runners with KVM. The debug test package adds an x86_64 JNI slice with
`-Pclambhook.android.managedDeviceAbi=x86_64`; the release AAR, APK, and
AAB remain ARM64-only. This separates portable emulator validation from the
locked production architecture and avoids relying on nested virtualization on
hosted ARM runners:

| API | Device profile | Required coverage |
| --- | --- | --- |
| 31 | Pixel 2 | Android 12 floor, consent, foreground service, TUN traffic |
| 33 | Pixel 6 | notification permission, process restart, revoke, reconnect |
| 36 | Pixel 6 | target-SDK behavior, always-on, updater, full regression |

```sh
make test-android-compatibility
```

The journeys cover consent; foreground-service notification; TUN traffic;
encrypted routes; reconnect; process restart; revoke; always-on behavior;
per-application routing; file/QR import; profiles; rules; prompts; capture;
licensing; and updater behavior. Each journey is independent, stops on crash or
freeze, and reports every action as passed, failed, or skipped. A physical
device may supplement these lanes but never replaces them.

## Manifest and signing

`ui/kotlin/app/src/main/AndroidManifest.xml` declares the launcher activity
and the `ss://`/`ssconf://` link handlers. The platform library manifest merges
its provider, `VpnConsentActivity`, QR activity, FileProvider, permissions, and
`ClambhookVpnService`. `ui/kotlin/app/build.gradle.kts` pins the application
ID, the ARM64 ABI filter, the version name/code (`VERSION`, `VERSION_CODE`),
and the release keystore inputs, which only the protected workflow supplies.

The protected release workflow inspects APK/AAB ABI contents, verifies APK and
bundle signatures, and writes SHA-256 files plus detached developer@jpfchang.org
GPG signatures for the APK, the AAB, their checksums, and the update manifest.
`scripts/verify-release-signatures.sh` then re-verifies all of them. The
in-app updater rejects a manifest or APK whose `.sig` does not verify against
the bundled release key. Do not store keystores or passwords in the repository.

## Runtime troubleshooting

- If the activity closes while the VPN remains connected, that is expected:
  the service owns the runtime.
- If consent is denied, request it again through the Profiles/Connect action;
  never launch the TUN runtime before consent succeeds.
- If native dependency provisioning fails, verify the NDK path and archive
  checksum instead of bypassing validation.
- If a hosted managed device cannot boot, confirm `/dev/kvm` is readable and
  writable and the exact `aosp_atd/x86_64` image and API are installed. Do not
  substitute API 30 or add x86_64 to a release artifact.
- Inspect `android layout` before using screen coordinates in a journey, and
  verify input fields are focused before sending text.
