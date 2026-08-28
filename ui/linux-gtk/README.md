<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

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
native daemon API. A reconnecting native WebSocket stream listens for
connection/rule events and coalesces bursty byte events into bounded status and
traffic refreshes. Its navigation exposes current status, traffic, policy
groups, prompts, encrypted DNS, opt-in HTTP capture, network conditioning,
listeners/servers, license information, and API settings. It can connect or
disconnect the daemon, switch active profiles, refresh the dashboard, and run
policy-group latency tests. Manual policy groups can switch chains; pending
prompts expose allow-once/session/until-quit/forever and block-forever actions
with optional host/port/protocol matching. Recent Silent Mode decisions appear
in a separate review list and can be promoted to session, until-quit, or
persisted forever rules with the same matching controls. Capture can be enabled
or disabled, and clearing all entries requires a destructive-action
confirmation. Bearer tokens are read from
`CLAMBHOOK_API_TOKEN` and are sent only in request headers.

Encrypted DNS and the network conditioner are editable. DNS changes preserve
the ordered upstream object array; conditioner changes cover enablement,
download/upload ceilings, latency, jitter, and loss. The display-independent
model validates their JSON shape and numeric input before the native daemon
performs semantic validation, transactional persistence, and live reload.

Capture rows support daemon-side text/method/error filtering and open a
redacted request/response detail window. Detail views expose bounded header and
body previews, truncation/encoding metadata, status/profile/chain context, and
safe cURL export to the desktop clipboard. Identifiers and filter values are
percent-escaped before they enter request paths. The non-executing cURL importer
rejects `@file` inputs and opens its parsed result in the editable composer. The
composer and one-click repeat action use the native daemon sender, which limits
requests to public HTTP(S) targets, pins validated DNS answers, disables proxy
environment variables, and revalidates same-origin redirects. HAR 1.2 export
writes the bounded, redacted archive through a native desktop save dialog.

The JSON-to-view models are kept outside GTK widgets so daemon-contract
fixtures can run without a display. Native configuration/rule editors, license
actions, and capture repeat/composition are implemented. This remains an
additive target until accessibility QA and package cutover are complete, after
which the existing Compose Desktop client can be removed.
