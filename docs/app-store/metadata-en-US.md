# App Store Metadata - en-US

## Pricing

- App price: Free.
- Paid Apps Agreement, tax, and banking must be complete in App Store Connect before review.
- ClambHook uses non-consumable In-App Purchases for premium access and paid feature updates. It does not use subscriptions.
- Initial 2-month access is server-controlled free access enforced by the license server. It does not use Apple's auto-renewable subscription introductory-offer mechanism, and the app must not be described as a time-limited evaluation build.
- Commercial setup checklist: `docs/app-store/commercial-setup.md`

## In-App Purchases

Create only these non-consumable In-App Purchases before submitting the build. Enable Family Sharing for each product in App Store Connect. Do not create placeholder products because App Store Connect product IDs cannot be reused after assignment.

| Display name | Product ID | Type | US base price |
| --- | --- | --- | --- |
| ClambHook Lifetime Unlock | `org.jpfchang.clambhook.unlock.lifetime` | Non-Consumable | USD 99.99 |
| ClambHook 2027 Feature Update | `org.jpfchang.clambhook.feature_update.2027` | Non-Consumable | USD 8.99 |

Future paid feature update products use the pattern `org.jpfchang.clambhook.feature_update.YYYY`.

The lifetime unlock includes features released through the purchase date plus one year. Each paid feature update extends that feature-release cutoff by one year. Existing purchased features remain enabled forever; only features released after the user's paid window require a USD 8.99 feature update. Bug fixes and security fixes remain included.

Purchase UI copy must include: "One-time unlock includes features released through DATE. Paid updates unlock later feature releases. Bug fixes/security fixes remain included."

## URLs

- Support URL: https://jpfchang.org/clambhook/support
- Privacy Policy URL: https://jpfchang.org/clambhook/privacy

## Compatibility

- Requires iOS 17.0 or later.

## Description

ClambHook is a private iPhone VPN/proxy router with metadata inspection for routing device traffic through user-managed proxy and VPN profiles. It supports ClambHook, OpenVPN, Shadowsocks, Tor, Trojan, and WireGuard profile types with local profile storage and on-device connection diagnostics.

Use ClambHook to import or edit profiles, start a local packet tunnel, choose the active route profile, and inspect metadata such as connection targets, routing decisions, byte counts, and hop status without third-party analytics, advertising SDKs, or tracking SDKs. Profile data, credentials, keys, connection metadata, logs, and diagnostics stay on the device unless you explicitly export them.

ClambHook v1 is not an HTTPS debugging proxy. It does not install a certificate authority, perform TLS MITM, store request or response bodies, export HAR files, or provide body-level redaction workflows.

## Keywords

VPN,proxy,WireGuard,OpenVPN,Shadowsocks,Tor,Trojan,network,privacy,tunnel

## Review Information

- Review contact: provide account-owner name, phone, and email in App Store Connect.
- Demo account: no app account login is required.
- Demo profile name: App Review Demo.
- Demo endpoint: `review-vpn.jpfchang.org:443`.
- Demo credentials: paste only in the App Review Notes field in App Store Connect; do not commit them.
- Demo profile template: `docs/app-store/app-review-demo-profile.toml.template`; render only with secret values during `make app-review-release-check` or the manual App Review Compliance workflow.
- Territory availability: v1 is United States only. In App Store Connect, select `Specific Countries or Regions` and select only `United States`; do not select `All Countries or Regions` or automatic future-country availability for v1.
- Notes: ClambHook creates a local VPN configuration through Network Extension and routes traffic according to user-managed profiles and rules. v1 inspection is metadata-only; the app does not install a certificate authority, perform TLS MITM, store request or response bodies, or export HAR files. The app is free to download and uses non-consumable In-App Purchases for premium access and paid feature updates. Family Sharing is enabled for these purchases. Initial 2-month access is granted and enforced by the license server, not Apple's auto-renewable subscription introductory-offer mechanism. No territories requiring VPN license information are selected for the v1 submission.

## Screenshots

Prepare iPhone screenshots for the current required App Store Connect iPhone display size, prioritizing 6.9-inch portrait screenshots. Capture at least:

- Dashboard with an active demo profile.
- Profile/routing view showing supported protocol configuration without real secrets.
- Settings or purchases screen showing privacy/support links and premium purchase products.
