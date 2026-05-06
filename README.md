<p align="center">
  <img src="clambhook-icon-1024.png" width="128" alt="clambhook icon">
</p>

# clambhook

A network utility.

## Build

- `make build`: build the daemon and TUI into `bin/`.
- `make test`: run Go tests.
- `make test-apple`: run the shared Swift package tests.
- `make build-apple`: build the daemon, generate `ui/apple/Clambhook.xcodeproj`, then build the macOS and iOS SwiftUI apps.
- `make test-android`: run the Android companion app unit tests.
- `make build-android`: build the Android companion app debug APK.
- `make test-windows`: run the Windows WinUI app unit tests on a machine with the .NET SDK and Windows SDK.
- `make build-windows`: build the Windows WinUI unpackaged debug app.

The HTTP API binds to `127.0.0.1:9090` by default. If you bind it to a
non-loopback address for iOS or remote control, start the daemon with
`-api-token` or `CLAMBHOOK_API_TOKEN`; clients must send
`Authorization: Bearer <token>`.

The Android app is a Jetpack Compose companion controller under `ui/android/`.
Its default API URL is `http://10.0.2.2:9090` for Android emulator access to a
daemon running on the host machine; physical devices should use the daemon host's
LAN URL and a bearer token.

The Windows app is an unpackaged WinUI 3 desktop controller under `ui/windows/`.
It can connect to a configured daemon API, use a bearer token, and launch a
configured `clambhook.exe` or a bundled executable placed next to the app.

## License

GNU General Public License v3.0
