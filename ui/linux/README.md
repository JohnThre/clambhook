# clambhook Linux

This is the native Linux desktop controller for clambhook. It uses Vala, GTK4,
libadwaita, libsoup 3, json-glib, libsecret, and Meson.

## Development

Install the GTK toolchain for your distribution, then run:

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
