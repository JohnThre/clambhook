# Clambhook GTK 4 client migration

This directory is the additive C/GTK 4 replacement for the existing
Kotlin/Compose Desktop client. Until the full feature-parity gate passes it is
built as `clambhook-linux-c`; packaging continues to ship the existing client.

Build and run the current migration slice with:

```sh
make build-linux-gtk
CLAMBHOOK_API_URL=http://127.0.0.1:9090 build-native/clambhook-linux-c
```

`make build-linux-gtk` builds the application and runs display-independent
GLib model fixtures plus a `--version` executable smoke. The supported
GNU/Linux GitHub matrix repeats those checks only on Trisquel 12, Rocky Linux
9, and AlmaLinux 9.

The controller uses `GtkApplication`, asynchronous libsoup requests, and the
native daemon API. Its navigation exposes current status, traffic, policy
groups, prompts, encrypted DNS, opt-in HTTP capture, network conditioning,
listeners/servers, license information, and API settings. It can connect or
disconnect the daemon, switch active profiles, refresh the dashboard, and run
policy-group latency tests. Manual policy groups can switch chains; pending
prompts expose allow-once/session/until-quit/forever and block-forever actions
with optional host/port/protocol matching. Capture can be enabled or disabled,
and clearing all entries requires a destructive-action confirmation. Bearer tokens are read from
`CLAMBHOOK_API_TOKEN` and are sent only in request headers.

The JSON-to-view models are kept outside GTK widgets so daemon-contract
fixtures can run without a display. This remains an additive target: editing
and accessibility parity, silent-mode decision promotion, capture filtering,
details/cURL import/export/repeat/composition, conditioner/DNS editing,
event-stream updates, licensing actions, and package cutover are still required
before the existing Compose Desktop client can be removed.
