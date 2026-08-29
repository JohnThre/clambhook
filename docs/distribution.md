# Distribution policy

The sole official binary distribution channel is
<https://github.com/JohnThre/clambhook/releases>.

- macOS: signed, notarized Apple-silicon DMG for macOS 14+.
- GNU/Linux: GPG-checksummed `.deb` and `.rpm` packages validated on Trisquel
  12, Rocky Linux 9, and AlmaLinux 9.
- Android: signed sideload APK for Android 12/API 31+.

Stable releases are created from verified signed `v*` tags. Approved betas are
GitHub prereleases and are also exposed through the rolling `beta` release for
update discovery. Assets are not mirrored to Cloudflare R2, app marketplaces,
Homebrew, package registries, or third-party download hosts.

The Clamber Cloud website provides product information and links to GitHub.
License purchase, renewal, activation, and device management remain on the
Swipht Group store and are independent of binary hosting.

A USD 49.99 one-time ClambHook license is required after the trial and includes one year of all updates.
Versions released on or before the update cutoff remain usable. A USD 9.99 renewal buys one additional update year. Each ClambHook License
covers a maximum of 3 concurrently active devices. Device seats can be deactivated
and transferred. Updates after the cutoff, including critical, bug, and security updates,
require a renewed update window. Checkout accepts Creem or NOWPayments, not PayPal.

Every binary ships with a SHA-256 file and armored GPG signature. macOS updates
also use Sparkle EdDSA signatures, Apple code signing, notarization, and
stapling; Android packages retain APK signature verification. The public GPG
key is attached to each release and tracked in `keys/`.

The macOS download is free and supports Apple Silicon Macs running macOS 14.0 or later.
Official downloads carry ClambHook trademarks. GPL-compliant forks may build
and redistribute the public core under their own branding without implying
official status.
