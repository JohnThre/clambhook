<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Shared JavaFX application

This is the only user interface shipped on Android and GNU/Linux. It targets
Java 17, JavaFX 21.0.12, Gluon's JavaFX 21.0.1 static SDK, and GluonFX 1.0.29.
The view covers connection state, profiles and configuration transfer,
activity, servers and chains, policies, rules, DNS/firewall/conditioner,
prompts, developer tools, settings, licensing, and updates.

```mermaid
flowchart TB
    views["Responsive JavaFX views"] --> client["Typed asynchronous RuntimeClient"]
    views --> services["PlatformServices capability boundary"]
    client --> backend{"Platform backend"}
    backend -->|"GNU/Linux authenticated<br/>HTTP + WebSocket"| daemon["Supervised C17 daemon"]
    backend -->|"Android Dalvik/JNI"| runtime["Service-owned C17 runtime"]
    services --> linux["systemd · polkit · secret-tool<br/>apt/dnf when configured"]
    services --> android["Kotlin AAR · VpnService<br/>files · QR · secure storage · updater"]
    android --> runtime
```

`RuntimeClient` owns the frozen JSON and control-route types. It never exposes
HTTP, JNI, or Android lifecycle objects to views. `PlatformServices` owns VPN
consent/lifecycle, file and QR operations, secure storage, clipboard/browser,
notifications, per-app routing, licensing, updates, and GNU/Linux daemon
supervision. The GNU/Linux transport accepts loopback HTTP(S) origins only,
uses the same bearer token for HTTP and WebSocket requests, validates the
WebSocket upgrade, and reconnects the event stream without blocking JavaFX.

Run `make test-javafx` from the repository root. The suite uses a real JavaFX
toolkit to exercise keyboard shortcuts, accessible names, responsive
navigation, 48-pixel interaction targets, contrast, failure/retry behavior,
and asynchronous transport. On headless GNU/Linux, the Make target starts Xvfb
automatically. Build targets do not rerun this suite; CI declares the test gate
as an explicit prerequisite before native-image jobs.

For GNU/Linux, set `GRAALVM_HOME` to the checksum-pinned Java 17 toolchain and
run `make build-linux`. The output is a self-contained x86_64 or aarch64 native
image under `target/gluonfx/<architecture>-linux/`; no JRE is packaged.
The AArch64 build deliberately targets GTK/X11 on Ubuntu and Fedora. GluonFX
1.0.29's Substrate backend otherwise treats every AArch64 Linux target as a
Raspberry Pi/Monocle device. Before compilation, the Make target downloads
Gluon's checksum-pinned non-Monocle static SDK and creates an isolated Maven
repository in `build-gluon-linux-aarch64/`. A deterministic helper changes
only Substrate's class-local backend selector; the target triplet remains
AArch64. The build rejects Monocle and DRM archives, and the distro harness
launches the result under Xvfb. Desktop native images include JavaFX's software
pipeline so virtual machines and systems without usable OpenGL retain a
renderer. No Gluon DRM extension code is downloaded, linked, or shipped.
The Maven configuration pins the application resources, reflection roots,
native-image arguments, and Android JNI boundary explicitly so reachability
metadata does not depend on host-side tracing. Its desktop and Android
profiles register only their own backend and platform-service implementations.
On GNU/Linux, license keys live in the system secret service through
`secret-tool`; the install ID and signed C-helper state are atomically written
with private permissions under `$XDG_CONFIG_HOME/clambhook/` (or
`~/.config/clambhook/`).
Update checks use `apt` or `dnf` only when the system administrator has
configured a signed package repository containing ClambHook. GitHub Releases
remains the official binary host, and official repository metadata is not yet
published.

For Android, first run `scripts/prepare-gluon-android.sh`, then run
`mvn -B -Pandroid gluonfx:build gluonfx:package`. The package step emits a
signed or development ARM64 APK and AAB and merges the Kotlin platform AAR.
