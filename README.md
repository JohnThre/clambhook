<p align="center">
  <img src="clambhook-icon-1024.png" alt="Clambhook official icon" width="160" height="160">
</p>

<h1 align="center">Clambhook</h1>

<p align="center">A private VPN/proxy router with metadata inspection.</p>

Clambhook v1 routes device traffic through user-managed proxy and VPN profiles and exposes metadata-only inspection for connection targets, routing decisions, byte counts, and hop status by default.

The daemon and terminal UI also include a separate opt-in developer mode for the explicit HTTP proxy listener. When `[developer] enabled = true`, clambhook can capture bounded HTTP request/response previews, export HAR, and perform HTTPS CONNECT MITM after you manually trust the generated local CA. Android/App Store paths remain metadata-only.

## End-user Distribution

The iPhone app is distributed only through the Apple App Store as a free download with non-consumable In-App Purchases for premium access and paid feature updates.

GitHub is source-only for end users. Do not publish or link end-user installers or package artifacts from GitHub, including `.dmg`, `.pkg`, `.apk`, `.aab`, `.ipa`, Homebrew formula releases, Debian packages, or macOS installer artifacts. Non-iPhone build and packaging targets in this repository are internal developer/QA workflows only.

## Donate

<a href="https://nowpayments.io/donation?api_key=5792a927-dd7d-4b0c-982b-584a7499ffc9" target="_blank" rel="noreferrer noopener">
    <img src="https://nowpayments.io/images/embeds/donation-button-black.svg" alt="Crypto donation button by NOWPayments">
</a> 
