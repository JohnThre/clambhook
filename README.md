<p align="center">
  <img src="clambhook-icon-1024.png" alt="Clambhook official icon" width="160" height="160">
</p>

<h1 align="center">Clambhook</h1>

<p align="center">A private connectivity client with local status views.</p>

Clambhook helps manage user-defined connectivity profiles and shows local status, counters, and recent activity summaries.

## macOS v1 Scope

The macOS app is proxy-only for v1. It launches the local daemon, exposes SOCKS5 and HTTP proxy listeners, and can optionally point macOS system HTTP, HTTPS, and SOCKS proxy settings at those listeners. It does not provide a macOS packet tunnel, full-device VPN, route-table ownership, DNS interception, or device-wide traffic capture. See `docs/macos-v1-scope.md`.

## End-user Distribution

The iOS and iPadOS app is distributed only through the Apple App Store as a free download with non-consumable In-App Purchases for premium access and paid feature updates. tvOS and visionOS builds are compile-first Apple targets until their platform-specific runtime surfaces are completed.

GitHub is source-only and view-only for end users. The source is proprietary to Pengfan Chang, all rights reserved, and may not be copied, modified, built, run, contributed to, redistributed, packaged, released, hosted, sublicensed, or used to create derivative works without separate prior written permission from Pengfan Chang.

Do not publish or link end-user installers or package artifacts from GitHub, including `.dmg`, `.pkg`, `.apk`, `.aab`, `.ipa`, Homebrew formula releases, Debian packages, or macOS installer artifacts. Other official builds are distributed only through Pengfan Chang's controlled channels. Only Pengfan Chang may distribute, publish, package, or release Clambhook artifacts.

The Android app should use Swift for common domain logic as much as practical in a future migration. Keep Kotlin for Android lifecycle, Compose UI, billing, services, storage, JNI/glue, and Gradle integration unless the Android Swift toolchain plan changes.

## Donate

<a href="https://nowpayments.io/donation?api_key=5792a927-dd7d-4b0c-982b-584a7499ffc9" target="_blank" rel="noreferrer noopener">
    <img src="https://nowpayments.io/images/embeds/donation-button-black.svg" alt="Crypto donation button by NOWPayments">
</a> 
