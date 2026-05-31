# App Store Metadata - en-US

## Pricing

- Paid app base price: USD 99.99.
- Paid Apps Agreement, tax, and banking must be complete in App Store Connect before review.
- No subscription, annual upgrade purchase, major-release paywall, or post-purchase feature gate.

## In-App Purchases

Create only these optional support purchases before submitting the build. They do not unlock app features, expire access, or gate future updates. Do not create any placeholder products because App Store Connect product IDs cannot be reused after assignment.

| Display name | Product ID | US base price |
| --- | --- | --- |
| Small Support | `org.jpfchang.clambhook.support.small` | USD 4.99 |
| Medium Support | `org.jpfchang.clambhook.support.medium` | USD 9.99 |
| Large Support | `org.jpfchang.clambhook.support.large` | USD 24.99 |

## URLs

- Support URL: https://jpfchang.org/clambhook/support
- Privacy Policy URL: https://jpfchang.org/clambhook/privacy

## Description

ClambHook is an iPhone VPN and network client for routing device traffic through user-managed proxy and VPN profiles. It supports ClambHook, OpenVPN, Shadowsocks, Tor, Trojan, and WireGuard profile types with local profile storage and on-device connection diagnostics.

Use ClambHook to import or edit profiles, start a local packet tunnel, choose the active route profile, and monitor connection status without third-party analytics, advertising SDKs, or tracking SDKs. Profile data, credentials, keys, connection metadata, logs, and diagnostics stay on the device unless you explicitly export them.

## Keywords

VPN,proxy,WireGuard,OpenVPN,Shadowsocks,Tor,Trojan,network,privacy,tunnel

## Review Information

- Review contact: provide account-owner name, phone, and email in App Store Connect.
- Demo profile name: App Review Demo.
- Demo endpoint: `review-vpn.jpfchang.org:443`.
- Demo credentials: paste only in the App Review Notes field in App Store Connect; do not commit them.
- Notes: ClambHook creates a local VPN configuration through Network Extension and routes traffic according to user-managed profiles and rules. Support purchases are optional and do not gate app functionality.

## Screenshots

Prepare iPhone screenshots for the current required App Store Connect iPhone display size, prioritizing 6.9-inch portrait screenshots. Capture at least:

- Dashboard with an active demo profile.
- Profile/routing view showing supported protocol configuration without real secrets.
- Settings or support screen showing privacy/support links.
