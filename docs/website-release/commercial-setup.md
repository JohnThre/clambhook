# Website Commercial Setup

This checklist is the source of truth for ClambHook direct-sale setup across
`store.clambercloud.com` and `store.swiphtgroup.com`.

## Account Prerequisites

- Confirm `store.clambercloud.com` serves ClambHook product, download, support, privacy, update manifest, appcast, and artifact routes.
- Confirm `store.swiphtgroup.com` has the `DB` binding and ClambHook license migrations applied.
- Confirm the ClambHook artifact R2 bucket is configured for download/update delivery.
- Confirm Creem product IDs are configured for the USD 99.99 license and USD 9.99 paid feature update.
- Confirm license grant email delivery is configured before accepting purchases.

## Product Page

- Product name: `ClambHook`.
- Product URL: `https://store.clambercloud.com/clambhook/`.
- Download URL: `https://store.clambercloud.com/clambhook/download/`.
- Buy URL: `https://store.swiphtgroup.com/clambhook/buy/`.
- License Portal URL: `https://store.swiphtgroup.com/clambhook/portal/`.
- Support URL: `https://store.clambercloud.com/clambhook/support/`.
- Privacy Policy URL: `https://store.clambercloud.com/clambhook/privacy/`.
- Distribution copy: direct download from `store.clambercloud.com`.

## License Products

Create and keep stable these product identifiers:

| Display name | Product ID | Type | US base price |
| --- | --- | --- | --- |
| ClambHook License | `org.jpfchang.clambhook.unlock.lifetime` | Direct-sale license | USD 99.99 |
| ClambHook for macOS 2027 Feature Update | `org.jpfchang.clambhook.feature_update.2027` | Direct-sale paid feature update | USD 9.99 |

Future paid feature update products use the pattern
`org.jpfchang.clambhook.feature_update.YYYY`.

The USD 99.99 license includes one year of feature updates. Versions released
during that year remain usable after the update year ends. Each license covers
up to 10 active devices across supported platforms. Device seats can be
deactivated and moved to another device. Paid feature updates extend the
feature-release window by one year from the renewal purchase date. Bug fixes and
security fixes remain included.

## Checkout

- Creem is the default checkout provider.
- The checkout page posts to `/api/clambhook/checkout`.
- License issuance and paid-update application happen from verified Creem webhook events.
- Paid update checkout requires an existing license key.

## Verification

- Confirm `https://store.clambercloud.com/api/clambhook/download` returns the current notarized macOS DMG while platform-specific artifacts are configured.
- Confirm `https://store.clambercloud.com/api/clambhook/update-manifest` returns the current update manifest.
- Confirm license checkout creates a license and sends the license email.
- Confirm paid update checkout extends the update window by one year from the later of the existing cutoff or renewal date.
- Confirm activation enforces 10 active devices across supported platforms.
- Confirm deactivation, reactivation, and transfer flows update device seats.
- Confirm the license portal can list devices and deactivate a selected device.
