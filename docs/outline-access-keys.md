<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Use an Outline access key

ClambHook is compatible with standard Outline access keys; it does not create
or sell them. Obtain a key from the person who manages your Outline server.
Static keys start with `ss://`. Basic dynamic keys start with `ssconf://` and
refer to an HTTPS configuration that can change over time.

## Import a key

- macOS: open **Profiles → Import Outline Access Key**, paste the key, or open
  an `ss:`/`ssconf:` link registered with ClambHook.
- GNU/Linux and Android: open **Profiles → Import Outline access key**. Paste
  from the clipboard or use **Scan key QR** where camera scanning is available.
  GNU/Linux also accepts registered `ss:` and `ssconf:` links.
- Terminal UI: press `i`, enter the key at the hidden prompt, review the
  credential-free preview, choose a profile name, and confirm.

ClambHook shows the TCP/UDP endpoint, cipher, prefix length, static/dynamic
kind, and proposed profile name before it changes the configuration. Opening a
link never imports or connects automatically. Static source URIs are discarded
after conversion. Dynamic source URLs remain transport credentials in the
local configuration and must not be shared in logs or support bundles.

## Refresh and connect

A dynamic profile is refreshed before every connection. If the new document
cannot be fetched or validated, ClambHook uses the last validated settings and
shows a stale warning. The initial import fails when no valid configuration is
available. To refresh manually, disconnect, select the profile, and choose
**Refresh Dynamic Profile** (or press `R` in the TUI).

Remote documents are limited to verified HTTPS public endpoints, same-origin
redirects, 15 seconds, and 64 KiB. HTTP and private/link-local destinations are
rejected. Supported documents are an `ss://` key, legacy Shadowsocks JSON or
YAML, or basic `tcpudp` YAML with Shadowsocks TCP and UDP entries. YAML anchors,
aliases, and merge keys are accepted.

## Troubleshooting

- Only `aes-128-gcm`, `aes-256-gcm`, and `chacha20-ietf-poly1305` are supported.
- SIP002 plugins are not supported. Ask the server manager for a standard key.
- WebSocket endpoints, `first-supported`, IP tables, nested dialers, and other
  advanced transport strategies require a simpler server configuration.
- A prefix cannot exceed the cipher salt. A warning appears when it leaves
  fewer than eight bytes of random salt.
- Name collisions are rejected. Choose a new profile name and import again.

See Outline's [access-key help](https://support.getoutline.org/client/getting-started/get-access-key/)
and [dynamic-key documentation](https://developer.getoutline.org/vpn/management/dynamic-access-keys/).
