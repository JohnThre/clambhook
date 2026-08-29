<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# macOS SwiftUI client

macOS 14 and later retains the native SwiftUI application. It communicates
only with the bundled production C17 daemon and ships the C TUI plus required
native dynamic libraries. No retired runtime or compatibility executable is
embedded.

```mermaid
flowchart LR
    swiftui["SwiftUI app and menu bar"] --> api["Frozen HTTP/WebSocket client"]
    helper["Privileged helper"] --> daemon["C17 clambhook daemon"]
    api --> daemon
    tools["C TUI and license helper"] --> daemon
    daemon --> tun["utun · routes · DNS"]
    daemon --> proxies["Encrypted proxy and VPN peers"]
```

Run `make build-apple` for an unsigned application build and `make test-apple`
for the shared Swift package tests. `make macos-release-contract-check`
validates the release product, licensing copy, and C-only bundle contract.
Signing, notarization, and publication remain confined to the protected
release workflow; local validation does not publish artifacts.
