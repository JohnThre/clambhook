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

The HTTP API binds to `127.0.0.1:9090` by default. If you bind it to a
non-loopback address for iOS or remote control, start the daemon with
`-api-token` or `CLAMBHOOK_API_TOKEN`; clients must send
`Authorization: Bearer <token>`.

## License

GNU General Public License v3.0
