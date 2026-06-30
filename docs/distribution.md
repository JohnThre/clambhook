# Distribution Policy

ClambHook end-user downloads are distributed only from
`store.clambercloud.com`. Checkout, license delivery, device-seat management,
and paid feature updates are handled by `store.swiphtgroup.com`.

## End-user Downloads

- Official product page: `https://store.clambercloud.com/clambhook/`.
- Download page: `https://store.clambercloud.com/clambhook/download/`.
- Buy or upgrade page: `https://store.swiphtgroup.com/clambhook/buy/`.
- License portal: `https://store.swiphtgroup.com/clambhook/portal/`.
- The public macOS DMG download is free and supports Apple Silicon Macs running macOS 14.0 or later.
- The first launch starts a one-calendar-month trial.
- A USD 99.99 ClambHook license includes one year of feature updates.
- Versions released during that year remain usable after the update year ends.
- Each license covers up to 10 active devices across supported platforms.
- Device seats can be deactivated so the license can be moved to another device.
- A USD 9.99 paid feature update unlocks later feature releases and extends the feature-release window by one year from the renewal purchase date.
- GitHub is source-only and view-only for end users.

The source is proprietary to Pengfan Chang, all rights reserved, and may not be
copied, modified, built, run, contributed to, redistributed, packaged, released,
hosted, sublicensed, or used to create derivative works without separate prior
written permission from Pengfan Chang.

## License Products

| Display name | Product ID | Type | US base price |
| --- | --- | --- | --- |
| ClambHook License | `org.jpfchang.clambhook.unlock.lifetime` | Direct-sale license | USD 99.99 |
| ClambHook for macOS 2027 Feature Update | `org.jpfchang.clambhook.feature_update.2027` | Direct-sale paid feature update | USD 9.99 |

Future paid feature update products use
`org.jpfchang.clambhook.feature_update.YYYY`.

The ClambHook license includes feature releases through the purchase date plus
one year. Versions released during that year remain usable after the update
window ends. Each paid feature update extends from the later of the previous
cutoff or the renewal purchase date, so a user can skip renewals for multiple
years and later pay USD 9.99 for one year of latest updates. Bug fixes and security fixes remain included.

## GitHub Release Rule

Do not release end-user installers or package artifacts on GitHub. This includes
`.dmg`, `.pkg`, `.apk`, `.aab`, Homebrew formula releases, Debian packages, and
macOS installer artifacts.

GNU/Linux, Windows, and Android build, package, and release targets remain
available for Pengfan Chang's internal developer QA unless a supported public
download channel is configured under `store.clambercloud.com`.

## macOS Scope

macOS uses daemon-backed routing. System Proxy mode may launch the bundled
daemon through the approved privileged helper or the user-session fallback,
expose local SOCKS5 and HTTP listeners, and optionally configure macOS system
HTTP, HTTPS, and SOCKS proxy settings to use those listeners. Traffic status and
history in System Proxy mode apply only to traffic that reaches the configured
clambhook proxy listeners.

Enhanced Mode launches the daemon through the privileged helper, creates a utun
interface, installs routes, and temporarily rewrites DNS when encrypted DNS is
enabled. It is the device-wide routing path for direct website builds and does
not use Apple's Network Extension or System Extension capabilities.

The full scope note is in `docs/macos-v1-scope.md`.
