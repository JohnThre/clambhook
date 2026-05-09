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

## Flatpak

Install Flatpak Builder and configure the Flathub remote, then run from the
repository root:

```sh
make build-linux-flatpak
```

The build writes `dist/linux/com.clambhook.Clambhook.flatpak`. The Flatpak uses
GNOME Platform 50, starts `clambhook-linux` as the desktop app, and installs the
bundled daemon at `/app/libexec/clambhook`. The daemon is built with
`CGO_ENABLED=0` so the installed Flatpak does not depend on host `libsodium` or
the repository C static library.

Run the local Flatpak smoke check with:

```sh
make test-linux-flatpak
```
