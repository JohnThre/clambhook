<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# macOS SwiftUI client

macOS 14 and later retains the native SwiftUI application. It communicates
only with the bundled production C17 daemon and ships the C TUI plus required
native dynamic libraries. No retired runtime or compatibility executable is
embedded.

```mermaid
flowchart LR
    swiftui["SwiftUI app<br/>menu bar · widget"] --> api["Typed authenticated<br/>HTTP/WebSocket client"]
    swiftui --> license["C license helper<br/>signed snapshot"]
    swiftui --> updater["Signed manifest<br/>Sparkle policy"]
    swiftui --> helper["Signed privileged helper"]
    tui["Bundled C TUI"] --> api
    helper --> daemon["Bundled C17 daemon"]
    api --> daemon
    daemon --> proxy["System proxy listeners"]
    daemon --> tun["utun · routes · DNS"]
    daemon --> peers["Encrypted proxy/VPN peers"]
```

Run `make build-apple` for an unsigned application build and `make test-apple`
for the shared Swift package tests. `make macos-release-contract-check`
validates the release product, licensing copy, and C-only bundle contract.
Signing, notarization, and publication remain confined to the protected
release workflow; local validation does not publish artifacts.
