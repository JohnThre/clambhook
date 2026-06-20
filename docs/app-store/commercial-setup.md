# Website Commercial Setup

This checklist is the source of truth for the ClambHook for macOS direct-sale
setup on jpfchang.org. It covers product-page, checkout, artifact, and license
backend configuration for the website distribution path.

## Account Prerequisites

- Confirm the production jpfchang.org deployment has the `DB` binding.
- Confirm the `CLAMBHOOK_ARTIFACTS` R2 bucket is configured.
- Confirm the ClambHook macOS license migrations have been applied.
- Confirm Creem product IDs are configured for the lifetime license and paid
  feature update.
- Confirm NowPayments credentials are configured if crypto checkout is enabled.
- Confirm license grant email delivery is configured before accepting purchases.

## Product Page

- Product name: `ClambHook for macOS`.
- Official URL: `https://jpfchang.org/clambhook/`.
- Buy URL: `https://jpfchang.org/clambhook/buy/`.
- Support URL: `https://jpfchang.org/clambhook/support/`.
- Privacy Policy URL: `https://jpfchang.org/clambhook/privacy/`.
- Platform copy: Apple Silicon Mac, macOS 14 or later.
- Distribution copy: direct website download from jpfchang.org.

Do not describe the current public release as an alternate-platform app,
subscription, or marketplace purchase. The public copy should point users to the
website download, the two-month trial, and direct-sale licensing.

## License Products

Create and keep stable these website product identifiers:

| Display name | Product ID | Type | US base price |
| --- | --- | --- | --- |
| ClambHook for macOS Lifetime License | `org.jpfchang.clambhook.unlock.lifetime` | Direct-sale license | USD 99.99 |
| ClambHook for macOS 2027 Feature Update | `org.jpfchang.clambhook.feature_update.2027` | Direct-sale paid update | USD 8.99 |

Future paid feature update products use the pattern
`org.jpfchang.clambhook.feature_update.YYYY`.

The lifetime license includes purchased features forever and includes one year
of new feature releases. Paid feature updates extend the feature-release window.
Bug fixes and security fixes remain included.

## Checkout

- Creem is the default checkout provider.
- NowPayments is the optional crypto checkout provider.
- The checkout page posts to `/api/clambhook/checkout`.
- License claim links return to `/clambhook/success`.
- License issue and paid-update application happen through `/api/clambhook/claim`
  after a valid checkout redirect or confirmed NowPayments request.

## Verification

- Confirm `/api/clambhook/download` returns the current notarized macOS DMG.
- Confirm `/api/clambhook/update-manifest` returns the current update manifest.
- Confirm lifetime checkout creates a license and sends the license email.
- Confirm paid update checkout requires an existing license key.
- Confirm activation accepts only macOS Apple Silicon device registrations.
- Confirm deactivation, reactivation, and transfer flows update device seats.
