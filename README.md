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
- `make release-macos`: build a self-contained Apple Silicon macOS app, sign it with a Developer ID Application certificate, notarize it, staple the ticket, and write `dist/macos/ClambhookMac-arm64.zip`.
- `make test-android`: run the Android companion app unit tests.
- `make build-android`: build the Android companion app debug APK.
- `make test-windows`: run the Windows WinUI app unit tests on a machine with the .NET SDK and Windows SDK.
- `make build-windows`: build the Windows daemon and WinUI unpackaged debug app.
- `make publish-windows`: publish a self-contained Windows app folder with the daemon, .NET runtime, and Windows App SDK runtime bundled.
- `make test-linux`: configure the GTK/libadwaita Meson project and run the Linux app GLib tests.
- `make build-linux`: build the daemon and compile the Linux GTK controller.

The HTTP API binds to `127.0.0.1:9090` by default. If you bind it to a
non-loopback address for iOS or remote control, start the daemon with
`-api-token` or `CLAMBHOOK_API_TOKEN`; clients must send
`Authorization: Bearer <token>`.

The Android app is a Jetpack Compose companion controller under `ui/android/`.
Its default API URL is `http://10.0.2.2:9090` for Android emulator access to a
daemon running on the host machine; physical devices should use the daemon host's
LAN URL and a bearer token.

The Windows app is an unpackaged WinUI 3 desktop controller under `ui/windows/`.
It can connect to a configured daemon API, use a bearer token, and launches the
bundled `clambhook.exe` by default. Use `make publish-windows` on a Windows
machine with the .NET SDK and Windows SDK to create a standalone publish folder;
WinUI 3 unpackaged apps cannot be distributed as a single `.exe`, so distribute
the whole publish directory.

The Linux app is a GTK4/libadwaita desktop controller under `ui/linux/`. It
stores settings at `$XDG_CONFIG_HOME/clambhook/linux-settings.json`, stores the
API token with Secret Service, and can launch a configured `clambhook` daemon or
a bundled executable placed next to the app. Development builds require Meson,
Vala, GTK4, libadwaita, libsoup 3, json-glib, libsecret, and gee.

## macOS Developer ID release

Direct macOS distribution requires a paid Apple Developer account with a
`Developer ID Application` certificate installed in the signing keychain. Create
a notarytool keychain profile once with:

```sh
xcrun notarytool store-credentials "clambhook-notary" --apple-id "<apple-id>" --team-id "<team-id>" --password "<app-specific-password>"
```

Then build the signed, notarized Apple Silicon app:

```sh
CLAMBHOOK_DEVELOPMENT_TEAM="<team-id>" NOTARYTOOL_PROFILE="clambhook-notary" make release-macos
```

The release script embeds the Go daemon in `Contents/MacOS`, embeds
`libsodium.26.dylib` in `Contents/Frameworks`, rewrites the daemon load path so
it does not depend on Homebrew, signs nested executables, notarizes the exported
app, staples the ticket, and verifies the result with `codesign`, `stapler`, and
`spctl`.

## License

GNU General Public License v3.0
