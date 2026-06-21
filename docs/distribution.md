# Distribution Policy

ClambHook's end-user macOS release is distributed only from jpfchang.org.

## End-user Downloads

- Official product page: `https://jpfchang.org/clambhook/`.
- The public installer is the Apple Silicon macOS build for macOS 14 or later.
- The website download starts a two-month free trial.
- A USD 99.99 direct-sale macOS license includes one year of feature updates on up to four active Apple Silicon Macs.
- Versions and features included during a user's paid update window remain usable after the update window ends.
- A USD 8.99 paid feature update unlocks new features released after the included first year and extends the update window by one year.
- Checkout and license delivery are handled on jpfchang.org through the configured direct-sale payment providers.
- License device listing, activation, deactivation, and transfer are available from `https://jpfchang.org/clambhook/portal/`.
- GitHub is source-only and view-only for end users.
- Public ClambHook copy must describe the macOS website download and direct-sale license. Do not describe current ClambHook distribution as an alternate-platform release, marketplace listing, subscription, or bundled purchase flow.

The source is proprietary to Pengfan Chang, all rights reserved, and may not be copied, modified, built, run, contributed to, redistributed, packaged, released, hosted, sublicensed, or used to create derivative works without separate prior written permission from Pengfan Chang.

## Website License Products

| Display name | Product ID | Type | US base price |
| --- | --- | --- | --- |
| ClambHook for macOS License | `org.jpfchang.clambhook.unlock.lifetime` | Direct-sale license | USD 99.99 |
| ClambHook for macOS 2027 Feature Update | `org.jpfchang.clambhook.feature_update.2027` | Direct-sale paid update | USD 8.99 |

Future paid feature update products use `org.jpfchang.clambhook.feature_update.YYYY`.

The macOS license includes feature releases through the purchase date plus one year. Each paid feature update extends that feature-release cutoff by one year. Existing included features remain enabled; only features released after the user's paid window require a USD 8.99 feature update. Bug fixes and security fixes remain included.

## GitHub Release Rule

Do not release end-user installers or package artifacts on GitHub. This includes `.dmg`, `.pkg`, `.apk`, `.aab`, `.ipa`, Homebrew formula releases, Debian packages, and macOS installer artifacts.

Non-macOS build, package, and release targets remain available for Pengfan Chang's internal developer QA only. Only Pengfan Chang may distribute, publish, package, or release ClambHook source code or artifacts.

## macOS Scope

macOS uses Network Extension packet tunnel mode for device-wide routing. The app embeds a packet tunnel system extension and configures a `NETunnelProviderManager` profile that passes packets to the shared tunnel runtime.

System proxy mode remains available as a fallback. In that mode the app may launch the bundled daemon through the approved privileged helper or the user-session fallback, expose local SOCKS5 and HTTP listeners, and optionally configure macOS system HTTP, HTTPS, and SOCKS proxy settings to use those listeners. Traffic status and history in fallback mode apply only to traffic that reaches the configured clambhook proxy listeners.

The full scope note is in `docs/macos-v1-scope.md`.
