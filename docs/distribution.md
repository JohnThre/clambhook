<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Distribution policy

The sole official binary distribution channel, once a release has been
published, is <https://clambercloud.com/clambhook/download/>, served from the `clambhook-artifacts` Cloudflare R2
bucket. GitHub hosts only source code; repository build outputs and CI reports
are not releases.

- macOS: signed, notarized Apple-silicon DMG for macOS 14+.
- GNU/Linux: x86_64/aarch64 GPG-checksummed `.deb` and `.rpm` packages, validated
  only on Ubuntu 24.04 LTS and Fedora Linux 44: `clambhook` (the GPL-3.0-only
  core daemon and license helper) and `clambhook-ui` (the proprietary
  Kotlin/Compose desktop controller and TUI).
- Android: signed ARM64 Kotlin/Compose APK for Android 12/API 31+, sideloaded;
  no App Bundle and no app-store distribution.

Stable releases are created from verified signed `v*` tags. Each release is
stored under `releases/<tag>/` and served at
`https://clambercloud.com/clambhook/releases/<tag>/`. The signed channel pointer
`channels/<stable|beta>/release.json` names the current release, and the
in-app updaters read their manifests and the Sparkle appcast through
`https://clambercloud.com/clambhook/channels/<channel>/`. Assets are not
mirrored to GitHub Releases, app marketplaces, Homebrew, package registries, or
third-party download hosts. The signed apt and dnf repositories are served at
`https://clambercloud.com/clambhook/linux/`, and the `clambhook` package
installs their source definitions.

The Clamber Cloud website hosts the downloads and links to the source on GitHub.
Subscription purchase, renewal, activation, cancellation, and device management remain on the
Swipht Group store and are independent of binary hosting.

New installations include a 7-day trial; already-started legacy trials retain
their original month-long end date. Continued use requires a recurring USD 79.99
annual subscription. Each paid term covers releases published during that term
and up to six concurrently active devices. Cancellation stops future billing at
the paid-through date; compatible versions released during paid terms remain
usable perpetually. Device seats can be deactivated and transferred, and a later
subscription can reuse the same key. Checkout accepts Creem or NOWPayments, not PayPal.

Every published package has a SHA-256 record or signed manifest covered by an
armored GPG signature. macOS updates also use Sparkle EdDSA signatures, Apple
code signing, notarization, and stapling; Android packages retain APK
signature verification. The public GPG key is attached to each release and
tracked in `keys/`.

The macOS download supports Apple Silicon Macs running macOS 14.0 or later.
Official downloads carry ClambHook trademarks. The apps are proprietary; the
core in this repository is GPL-3.0-only, and the apps ship its license texts and
a pointer to this source. GPL-compliant forks may build and redistribute the
public core under their own branding without implying official status.
