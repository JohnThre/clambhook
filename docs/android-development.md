# Android development

Google's [`android`](https://developer.android.com/cli) CLI is the default tool
for ClambHook Android development. It manages the SDK and emulators and builds,
deploys, and launches the app on a device or emulator. Gradle is still used for
unit tests, lint, and release assembly — the `android` CLI has no equivalent
commands for those — and [gomobile](https://pkg.go.dev/golang.org/x/mobile)
generates the embedded daemon AAR.

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
android sdk install "platforms;android-34" "build-tools;34.0.0" "platform-tools"
android sdk install "ndk;27.0.12077973"      # gomobile AAR build needs an NDK
android sdk list --all                        # browse available packages
```

> The exact NDK version is whatever `gomobile bind` resolves for your toolchain;
> install a recent NDK (27.x) if the AAR build in the next step complains.

## Emulators

```sh
android emulator list                         # available AVDs
android emulator create --name=clambhook --package="system-images;android-34;google_apis;arm64-v8a"
android emulator start clambhook              # blocks until the AVD is booted
android emulator stop clambhook
android emulator remove clambhook
```

## Build and run (default dev loop)

The Android app embeds the Go daemon through a gomobile-built AAR. Build the AAR
once (and again whenever the daemon or `pkg/mobile` changes), then use the
`android` CLI to build, deploy, and launch on a connected device or emulator:

```sh
make build-android-mobile-aar                 # gomobile bind → ui/android/app/libs/
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
```

The release pipeline (`scripts/release-android.sh`) builds the AAR with
`make build-android-mobile-aar`, assembles the release APK with Gradle, then
checksums, GPG-signs, and writes the update manifest — see
[`docs/website-release/release-runbook.md`](website-release/release-runbook.md)
and [`docs/release-validation.md`](release-validation.md).

## Local CI

`scripts/ci-local.sh android` runs the AAR build, unit tests, lint, and debug
build headlessly. The on-device smoke uses an Android SDK Emulator (AVD) — Apple
`container` is Linux-only and cannot run Android. Set `CI_LOCAL_ANDROID_AVD=<name>`
to boot that AVD and run `make run-android` against it:

```sh
android emulator create --name=clambhook --package="system-images;android-34;google_apis;arm64-v8a"
CI_LOCAL_ANDROID_AVD=clambhook scripts/ci-local.sh android
```

The section boots the AVD (`android emulator start`), runs the app, and leaves
the emulator running; stop it with `android emulator stop clambhook`. See
[`docs/release-validation.md`](release-validation.md).
