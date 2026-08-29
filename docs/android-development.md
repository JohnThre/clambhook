<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Android development

ClambHook supports Android 12/API 31 through API 36 on ARM64. The application
ID is `org.jpfchang.clambhook`, `minSdk` is 31, and `targetSdk` is 36. API 30
and 32-bit release ABIs are not supported.

## Ownership boundaries

The user-facing application is the shared JavaFX 21.0.12 UI built by
GluonFX 1.0.29. `ui/android` builds a non-UI Kotlin AAR named
`clambhook-android-platform`.

The AAR owns:

- VPN consent and `VpnService` foreground lifecycle;
- the single JNI-backed C17 runtime and TUN descriptor;
- process restart, revoke, always-on, and reconnect behavior;
- per-application allow/bypass routing;
- file import/export, QR scan/share, and FileProvider updates;
- encrypted secure storage, clipboard/browser, notifications, licensing, and
  updater integration.

The JavaFX activity attaches through `AndroidDalvikBridge` and
`GluonPlatformFacade`. Activity destruction closes only the view attachment;
it does not stop or destroy the service-owned runtime.

## Toolchain

- Java 17
- Android Gradle Plugin and Kotlin versions pinned in `ui/android`
- compileSdk/targetSdk 36
- Android NDK `27.0.12077973`
- CMake 3.22.1
- Maven, GraalVM for JDK 17, JavaFX 21.0.12, and GluonFX 1.0.29
- OpenSSL 3.5.8 and curl 8.18.0 source archives verified by SHA-256

Use the Android CLI for local SDK and device management:

```sh
android info
android sdk list --all 'platforms*'
android sdk install platforms/android-36 build-tools/36.0.0
```

The Gradle build will provision the pinned native sources into
`ui/android/.native-deps`. Release packages contain only `arm64-v8a` native
libraries.

## Build and test

```sh
make test-javafx
make test-android

# Set this to the output of the checksum-pinned provisioner.
export GRAALVM_HOME=/path/to/graalvm-jdk-17
export JAVA_HOME="$GRAALVM_HOME"
export PATH="$GRAALVM_HOME/bin:$PATH"

make build-android
make build-android-release
```

`make test-android` runs Kotlin unit tests, Android lint, ARM64 JNI/C
compilation, and release AAR assembly. `make build-android` copies that AAR
into Gluon's Android project and builds the JavaFX native application.
`make build-android-release` packages both an APK and AAB.

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

Hosted CI is authoritative and runs `aosp_atd/arm64-v8a` images:

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

## Manifest and native-image metadata

`ui/javafx/src/android/AndroidManifest.xml` supplies the Gluon activity and
product identifiers. The platform AAR manifest merges its provider,
`VpnConsentActivity`, QR activity, FileProvider, permissions, and
`ClambhookVpnService`. Maven configuration pins:

- JavaFX resources and CSS;
- JNI and Dalvik bridge classes;
- the static C bridge archive;
- app label, version name/code, and application ID;
- release keystore inputs supplied only by the protected workflow.

The protected release workflow inspects APK/AAB ABI contents, verifies APK and
bundle signatures, produces SHA-256 files and GPG signatures, and writes the
signed update manifest. Do not store keystores or passwords in the repository.

## Runtime troubleshooting

- If the activity closes while the VPN remains connected, that is expected:
  the service owns the runtime.
- If consent is denied, request it again through the Profiles/Connect action;
  never launch the TUN runtime before consent succeeds.
- If native dependency provisioning fails, verify the NDK path and archive
  checksum instead of bypassing validation.
- If a managed device cannot boot, confirm the exact
  `aosp_atd/arm64-v8a` image and API are installed. Do not substitute API 30 or
  a release ABI.
- Inspect `android layout` before using screen coordinates in a journey, and
  verify input fields are focused before sending text.
