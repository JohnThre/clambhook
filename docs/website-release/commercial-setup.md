<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Website Commercial Setup

This checklist is the source of truth for ClambHook subscription setup across
`clambercloud.com` and `store.swiphtgroup.com`.

## Account Prerequisites

- Confirm `clambercloud.com` serves ClambHook product, download guidance, support, and privacy routes without commerce code.
- Confirm `store.swiphtgroup.com` has the `DB` binding and ClambHook license migrations applied.
- Confirm the official ClambHook GitHub Releases page and latest asset URLs are public.
- Confirm test-mode Creem and NOWPayments recurring annual products are configured for USD 79.99/year.
- Confirm a dedicated license-key derivation secret is configured separately from provider webhook secrets and the public donation API key.
- Confirm license grant email delivery is configured before accepting purchases.

## Product Page

- Product name: `ClambHook`.
- Product URL: `https://clambercloud.com/clambhook/`.
- Download URL: `https://clambercloud.com/clambhook/download/`.
- Buy URL: `https://store.swiphtgroup.com/clambhook/buy/`.
- License Portal URL: `https://store.swiphtgroup.com/clambhook/portal/`.
- Support URL: `https://clambercloud.com/clambhook/support/`.
- Privacy Policy URL: `https://clambercloud.com/clambhook/privacy/`.
- Distribution copy: signed downloads from GitHub Releases.

## Annual Product and Compatibility Entitlements

Create one provider annual product/plan at USD 79.99/year. Keep these signed
compatibility identifiers stable for older clients:

| Display name | Product ID | Type | US base price |
| --- | --- | --- | --- |
| First verified paid term | `org.jpfchang.clambhook.unlock.lifetime` | Lifetime fallback entitlement | USD 79.99 annual term |
| Each later paid term | `org.jpfchang.clambhook.feature_update` | Paid-through extension | USD 79.99 annual term |

New installations receive a 7-day trial; already-started legacy trials retain
their original month-long end date. Each verified annual payment adds one paid
term. Compatible releases from paid terms remain usable perpetually. One key
covers a maximum of 6 concurrently active devices and may be reused for renewal,
resubscription, or a provider change.

## Checkout

- Creem and NOWPayments are the only accepted and advertised ClambHook purchase payment providers. Do not offer PayPal.
- The checkout page posts `{ provider, email, licenseKey? }` to `/api/clambhook/checkout`.
- License issuance and annual extension happen only from verified paid Creem or NOWPayments webhook events.
- Email is required for license delivery; an existing key is optional for renewal, resubscription, or provider changes.
- Cancellation schedules the end of future billing and never revokes an already-paid term.
- Refunds revoke only the affected transaction; webhook retries are deduplicated by provider event or transaction ID.

## Device seat limit

- The current terms cover a maximum of 6 concurrently active devices per license.
- The limit is enforced per license from `clambhook_licenses.max_active_devices`
  in D1 and by the `clambhook_device_limit_insert` / `clambhook_device_limit_reactivate`
  triggers. New licenses default to 6.
- Append-only migration `032_clambhook_annual_subscriptions` expands existing
  licenses to six seats, recreates the trigger fallback at six, adds subscription
  state and transaction revocation timestamps, and preserves historical records.

## Verification

- Confirm `https://github.com/JohnThre/clambhook/releases/latest` exposes the current notarized DMG, signed APK, `.deb`, `.rpm`, checksums, signatures, and manifests.
- Confirm the Clamber Cloud download page links to GitHub Releases.
- Confirm each provider's test-mode annual payment creates or extends a provider-neutral license and sends the email.
- Confirm cancellation, lapse, resubscription, provider switching, refunds, chargebacks, and webhook replay behavior.
- Confirm activation enforces 6 active devices across supported platforms.
- Confirm deactivation, reactivation, and transfer flows update device seats.
- Confirm the portal shows subscription status and paid-through date, schedules cancellation, lists devices, and deactivates a selected device.
- Do not enable live billing until both providers pass end-to-end test-mode verification.
