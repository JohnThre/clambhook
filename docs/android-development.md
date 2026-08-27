# Android development

Google's [`android`](https://developer.android.com/cli) CLI is the default tool
for ClambHook Android development. It manages the SDK and emulators and builds,
deploys, and launches the app on a device or emulator. Gradle is still used for
unit tests, lint, release assembly, and the API compatibility matrix — the
`android` CLI has no equivalent commands for those. Gradle builds and packages
`libclambhook_jni.so` with the NDK; the production VPN service selects that
C/JNI runtime and packages no gomobile AAR. See
[`c-migration.md`](c-migration.md).

Building, running, and testing require prior written permission from Pengfan
Chang; see [`LICENSE`](../LICENSE). These instructions are for the author and
authorized parties.

## Install the `android` CLI

```sh
# macOS (Apple silicon):
curl -fsSL https://dl.google.com/android/cli/latest/darwin_arm64/install.sh | bash
# macOS (Intel):
curl -fsSL https://dl.google.com/android/cli/latest/darwin_x86_64/install.sh | bash
# Linux:
curl -fsSL https://dl.google.com/android/cli/latest/linux_x86_64/install.sh | bash

android --version
```

Initialize the environment and confirm the SDK location:

```sh
android init
android info
```

`ANDROID_HOME` defaults to `~/Library/Android/sdk` on macOS (the Makefile uses
the same default). Override with `android --sdk=/path/to/sdk …` or the
`ANDROID_HOME` environment variable.

## SDK and NDK

Provision the platform, build tools, and NDK the project needs:

```sh
android sdk install "platforms;android-31" "platforms;android-33" "platforms;android-36" "platform-tools"
android sdk install "ndk;27.0.12077973"      # mobile native builds need an NDK
android sdk list --all                        # browse available packages
```

The Gradle native build currently resolves NDK `27.0.12077973` and CMake
`3.22.1`. It downloads the official OpenSSL 3.5.8 LTS source archive, verifies
the pinned SHA-256 digest, and caches static API 31 libraries for each ABI under
the ignored `ui/android/.native-deps/` directory. See
`third_party/openssl/README.clambhook.md` for provenance and update steps.

## Emulators

```sh
android emulator list                         # available AVDs
android emulator create --name=clambhook --package="system-images;android-31;google_apis;arm64-v8a"
android emulator start clambhook              # blocks until the AVD is booted
android emulator stop clambhook
android emulator remove clambhook
```

## Build and run (default dev loop)

The Android GUI is Kotlin with Jetpack Compose and supports Android 12 and
newer (`minSdk = 31`). The production VPN uses the native C/JNI packet runtime.
Use the `android` CLI to build, deploy, and launch on a connected device or
emulator:

The packaged C/JNI façade already covers native configuration, dashboard
status/profile/server/rule reads, profile selection, and compiled-rule route
explanations. It now also builds the shared lwIP IPv4/IPv6 core for every ABI
and accepts raw packets through JNI, returning native stack output through the
Kotlin packet-writer callback. The JNI runtime owns its independent packet
timer, resolves direct, named-chain, and policy-group TCP/UDP decisions through
the shared C protocol layer, and transactionally rebuilds those rules when the
active profile changes. Native DoT now intercepts UDP/53 and returns SERVFAIL
when its encrypted upstream cannot be reached. DoH configuration fails closed
until libcurl is linked into the Android native build. Requested-profile reads
share the strict C control contract, and the JNI test calls all three native
AEAD families in the statically linked OpenSSL build. A physical Pixel 3a XL
running Android 12/API 32 has passed focused instrumentation, but it is optional;
GitHub's managed API 31/33/36 devices are authoritative.

```sh
make build-android-native                     # NDK builds libclambhook_jni.so
make run-android                              # cd ui/android && android run
```

`android run` builds the Gradle project, installs the APK on the connected
device/emulator, and launches the main activity. Pass `--debug` for a debug
build, `--device=<serial>` to target a specific device, or `--activity=<name>`
to launch a different activity (`android run --help` for the full set).

Other day-to-day commands:

```sh
android install --apks=ui/android/app/build/outputs/apk/debug/app-debug.apk
android screenshot --output=/tmp/clambhook.png
android screen capture                         # capture the current screen
android layout                                 # dump the UI layout tree (JSON)
android layout --diff                          # elements changed since the last dump
android docs search "VPN service lifecycle"    # search Android developer docs
```

`android layout` is usually faster than a screenshot for debugging UI issues.

## Tests, lint, and release assembly (Gradle)

The `android` CLI has no unit-test, lint, or release-build command, so those
stay on Gradle:

```sh
make test-android        # ./gradlew :app:testDebugUnitTest
make lint-android        # ./gradlew :app:lintDebug
make build-android       # ./gradlew :app:assembleDebug   (headless build, no device)
make build-android-release   # ./gradlew :app:assembleRelease  (signed release APK)
make test-android-compatibility # Compose instrumentation on API 31, 33, and 36
```

The managed-device target packages only `arm64-v8a` for the arm64 AOSP
emulators. This keeps the test APK below emulator
installation timeouts; normal debug and release builds retain their full ABI
set.

The compatibility target uses Gradle build-managed AOSP Pixel 2 devices named
`pixel2Api31`, plus Pixel 6 devices named `pixel6Api33` and `pixel6Api36`. It is the required Android
cutover gate, not a substitute for unit tests or lint.

The release pipeline (`scripts/release-android.sh`) assembles the native C/JNI
release APK with Gradle, then checksums, GPG-signs, and writes the update
manifest — see
[`docs/website-release/release-runbook.md`](website-release/release-runbook.md)
and [`docs/release-validation.md`](release-validation.md).

Protected GitHub CD builds without the Android keystore, signs the completed
package with the Android SDK `apksigner`, verifies that signature, deletes the
temporary keystore, and uploads only to R2. Environment setup and secret names
are documented in [`github-cicd.md`](github-cicd.md).

## Local CI

`scripts/ci-local.sh android` runs the AAR build, unit tests, lint, and debug
build headlessly. The on-device smoke uses an Android SDK Emulator (AVD). Set `CI_LOCAL_ANDROID_AVD=<name>`
to boot that AVD and run `make run-android` against it:

```sh
android emulator create --name=clambhook --package="system-images;android-31;google_apis;arm64-v8a"
CI_LOCAL_ANDROID_AVD=clambhook scripts/ci-local.sh android
```

The section boots the AVD (`android emulator start`), runs the app, and leaves
the emulator running; stop it with `android emulator stop clambhook`. See
[`docs/release-validation.md`](release-validation.md).
