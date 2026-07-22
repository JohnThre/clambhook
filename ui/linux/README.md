# clambhook GNU/Linux

This is the native GNU/Linux desktop controller for clambhook. It is written in
Kotlin and uses Compose Multiplatform (JVM Desktop) for the UI, OkHttp for HTTP
and WebSocket, and kotlinx.serialization for JSON. It targets JDK 17+ and
builds with Gradle.

## Development

Install JDK 17 or later, then run from `ui/linux`:

```sh
./gradlew test
./gradlew installDist
```

From the repository root, `make test-linux` runs the Kotlin unit test suite and
`make build-linux` builds the daemon before compiling and staging the Linux app.
`make install-linux DESTDIR=/tmp/stage PREFIX=/usr` stages the desktop app,
desktop file, AppStream metadata, icon, and a private daemon helper under
`libexec`.

Settings are stored in `$XDG_CONFIG_HOME/clambhook/linux-settings.json`, falling
back to `~/.config/clambhook/linux-settings.json`. The API bearer token is stored
through the platform Secret Service via `secret-tool` (libsecret/GNOME Keyring).

## Debian Packaging

The Linux desktop app installs as `com.clambhook.Clambhook` and includes:

- `clambhook-linux`, the Kotlin/Compose desktop controller
- `clambhook`, installed as a private helper under `libexec`
- a desktop launcher, AppStream metadata, and the hicolor app icon

The only supported GNU/Linux package installer is the Debian package under
`debian/`. Build it with `dpkg-buildpackage -us -uc -b` on a Debian-compatible
system with the listed build dependencies installed.

## Daemon startup

When the app launches a daemon, it resolves the executable in this order:

1. the configured daemon path
2. `clambhook` found on `PATH`
3. a `clambhook` executable adjacent to the app binary

The API endpoint setting remains a URL for the controller. Before launching the
daemon, the app converts it to the daemon's listen address form, for example
`http://127.0.0.1:9090` becomes `127.0.0.1:9090`.

## Device-wide TUN mode

The daemon can run a Linux-only TUN listener when `[profile.listen.tun]` is
enabled in the config. This mode requires root or `CAP_NET_ADMIN` because the
daemon creates a TUN interface, assigns addresses, installs split default
routes, and removes those routes again when the listener stops.

TUN mode captures routed TCP/UDP device traffic and routes it through the
selected clambhook chain. Dashboard events expose metadata and byte counts only;
packet payloads are not stored or streamed by default.