# clambhook Linux

This is the native Linux desktop controller for clambhook. It uses Vala, GTK4,
libadwaita, libsoup 3, json-glib, libsecret, and Meson.

## Development

Install the GTK toolchain for your distribution, including `valac`, `meson`,
GTK4, libadwaita, gee, json-glib, libsoup 3, and libsecret development
packages, then run:

```sh
meson setup builddir --reconfigure
meson test -C builddir
meson compile -C builddir
```

From the repository root, `make test-linux` runs the Meson test suite and
`make build-linux` builds the daemon before compiling the Linux app.

Settings are stored in `$XDG_CONFIG_HOME/clambhook/linux-settings.json`, falling
back to `~/.config/clambhook/linux-settings.json` through GLib. The API bearer
token is stored through Secret Service via libsecret.

## Device-wide TUN mode

The daemon can run a Linux-only TUN listener when `[profile.listen.tun]` is
enabled in the config. This mode requires root or `CAP_NET_ADMIN` because the
daemon creates a TUN interface, assigns addresses, installs split default
routes, and removes those routes again when the listener stops.

TUN mode captures routed TCP/UDP device traffic and routes it through the
selected clambhook chain. Dashboard events expose metadata and byte counts only;
packet payloads are not stored or streamed by default.
