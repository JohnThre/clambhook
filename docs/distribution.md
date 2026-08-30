<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Distribution policy

The sole official binary distribution channel, once a protected release has
completed, is <https://github.com/JohnThre/clambhook/releases>. An empty release
page means that no official binary is currently published; repository build
outputs and CI reports are not releases.

- macOS: signed, notarized Apple-silicon DMG for macOS 14+.
- GNU/Linux: x86_64/aarch64 GPG-checksummed `.deb` and `.rpm` packages with a
  self-contained JavaFX/Gluon native image, validated only on Ubuntu 24.04 LTS
  and Fedora Linux 44.
- Android: signed ARM64 Gluon APK and AAB for Android 12/API 31+.

Stable releases are created from verified signed `v*` tags. Approved betas are
GitHub prereleases and are also exposed through the rolling `beta` release for
update discovery. Assets are not mirrored to Cloudflare R2, app marketplaces,
Homebrew, package registries, or third-party download hosts. The GNU/Linux UI
can use `apt` or `dnf` only when the administrator has independently configured
a signed repository containing ClambHook; official repository metadata is not
currently published.

The Clamber Cloud website provides product information and links to GitHub.
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
code signing, notarization, and stapling; Android packages retain APK/AAB
signature verification. The public GPG key is attached to each release and
tracked in `keys/`.

The macOS download supports Apple Silicon Macs running macOS 14.0 or later.
Official downloads carry ClambHook trademarks. GPL-compliant forks may build
and redistribute the public core under their own branding without implying
official status.
