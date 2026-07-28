# Website Commercial Setup

This checklist is the source of truth for ClambHook direct-sale setup across
`store.clambercloud.com` and `store.swiphtgroup.com`.

## Account Prerequisites

- Confirm `store.clambercloud.com` serves ClambHook product, download, support, privacy, update manifest, appcast, and artifact routes.
- Confirm `store.swiphtgroup.com` has the `DB` binding and ClambHook license migrations applied.
- Confirm the ClambHook artifact R2 bucket is configured for download/update delivery.
- Confirm Creem and NOWPayments product IDs are configured for the USD 49.99 license and USD 9.99 update-year renewal.
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
| ClambHook License | `org.jpfchang.clambhook.unlock.lifetime` | Direct-sale license | USD 49.99 |
| ClambHook Update Year | `org.jpfchang.clambhook.feature_update` | Direct-sale update-year renewal | USD 9.99 |

A single provider-neutral renewal SKU applies to each additional update year;
there is no per-year product identifier.

The USD 49.99 one-time license is required after the one-calendar-month trial
and includes one year of all updates from the purchase date. Versions released
on or before the update cutoff remain usable after the cutoff. Each license
covers a maximum of 3 concurrently active devices across supported platforms.
Device seats can be deactivated and moved to another device. A USD 9.99 renewal
buys one additional update year, extending from the later of the current cutoff
or the renewal payment date. After the cutoff, no later updates are included,
including critical, bug, and security updates.

## Checkout

- Creem and NOWPayments are the only accepted and advertised ClambHook purchase payment providers. Do not offer PayPal.
- The checkout page posts to `/api/clambhook/checkout`.
- License issuance and update-year renewal application happen from verified Creem or NOWPayments webhook events.
- Update-year renewal checkout requires an existing license key.
- The live Creem license product is the USD 49.99 one-time SKU. Creem prices are
  immutable, so a price change means creating a new product and repointing
  `CLAMBHOOK_CREEM_LICENSE_PRODUCT_ID` (Cloudflare Pages secret) at it. The
  update-year renewal remains the USD 9.99 one-time SKU.

## Device seat limit

- The current terms cover a maximum of 3 concurrently active devices per license.
- The limit is enforced per license from `clambhook_licenses.max_active_devices`
  in D1 and by the `clambhook_device_limit_insert` / `clambhook_device_limit_reactivate`
  triggers. New licenses default to 3.
- To force all end users onto the current limit, migration
  `015_clambhook_3_device_seats` sets every existing license to 3 and recreates
  the triggers with a fallback of 3. The downgrade is graceful: no device is
  deactivated, but licenses already over the limit cannot add or reactivate a
  seat beyond 3 until they deactivate one.

## Verification

- Confirm `https://store.clambercloud.com/api/clambhook/download` returns the current notarized macOS DMG while platform-specific artifacts are configured.
- Confirm `https://store.clambercloud.com/api/clambhook/update-manifest` returns the current update manifest.
- Confirm license checkout creates a license and sends the license email.
- Confirm the USD 9.99 renewal extends the update window by one year from the later of the current cutoff or renewal payment date.
- Confirm activation enforces 3 active devices across supported platforms.
- Confirm deactivation, reactivation, and transfer flows update device seats.
- Confirm the license portal can list devices and deactivate a selected device.
