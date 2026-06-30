<p align="center">
  <img src="clambhook-icon-1024.png" alt="Clambhook official icon" width="160" height="160">
</p>

<h1 align="center">Clambhook</h1>

<p align="center">A private connectivity client with local status views.</p>

Clambhook helps manage user-defined connectivity profiles and shows local status, counters, and recent activity summaries.

## macOS Scope

The macOS app no longer depends on Apple's restricted Network Extension or System Extension approval path. It supports System Proxy mode for apps that honor macOS HTTP, HTTPS, and SOCKS proxy settings, plus Enhanced Mode, which runs the privileged daemon with a utun interface for device-wide routing. See `docs/macos-v1-scope.md`.

## End-user Distribution

The end-user macOS app is distributed only from `https://store.clambercloud.com/clambhook/` as a free public DMG download for Apple Silicon Macs running macOS 14 or later. Users can try ClambHook for one calendar month, then buy a USD 99.99 ClambHook license from `https://store.swiphtgroup.com/clambhook/buy`. The license includes one year of feature updates; versions released during that year remain usable after the update year ends; it covers up to 10 active devices across supported platforms and seats can be deactivated for transfers. A USD 9.99 paid feature update unlocks later feature releases and extends the update window by one year from the renewal date.

Official public website routes:

- Product: `https://store.clambercloud.com/clambhook/`
- Download: `https://store.clambercloud.com/clambhook/download/`
- Buy or upgrade: `https://store.swiphtgroup.com/clambhook/buy/`
- License portal: `https://store.swiphtgroup.com/clambhook/portal/`
- License terms: `https://store.swiphtgroup.com/clambhook/license/`
- Privacy policy: `https://store.clambercloud.com/clambhook/privacy/`
- Support: `https://store.clambercloud.com/clambhook/support/`

ClambHook is not distributed to end users through app marketplaces, GitHub Releases, Homebrew, package registries, or third-party mirrors. Other platform builds are internal developer QA targets until a separate distribution plan is approved.

GitHub is source-only and view-only for end users. The source is proprietary to Pengfan Chang, all rights reserved, and may not be copied, modified, built, run, contributed to, redistributed, packaged, released, hosted, sublicensed, or used to create derivative works without separate prior written permission from Pengfan Chang.

Do not publish or link end-user installers or package artifacts from GitHub, including `.dmg`, `.pkg`, `.apk`, `.aab`, Homebrew formula releases, Debian packages, or macOS installer artifacts. Other official builds are distributed only through Pengfan Chang's controlled channels. Only Pengfan Chang may distribute, publish, package, or release Clambhook artifacts.

The Android app should use Swift for common domain logic as much as practical in a future migration. Keep Kotlin for Android lifecycle, Compose UI, billing, services, storage, JNI/glue, and Gradle integration unless the Android Swift toolchain plan changes.

## Donate

<a href="https://nowpayments.io/donation?api_key=5792a927-dd7d-4b0c-982b-584a7499ffc9" target="_blank" rel="noreferrer noopener">
    <img src="https://nowpayments.io/images/embeds/donation-button-black.svg" alt="Crypto donation button by NOWPayments">
</a> 
