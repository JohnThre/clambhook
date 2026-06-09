# App Store Connect Commercial Setup

This checklist is the source of truth for the v1 iPhone commercial setup in App Store Connect. It covers account-side actions only; repository release builds still use `make archive-iphone`.

## Account Prerequisites

- Confirm the Paid Apps Agreement is active.
- Confirm tax and banking are complete.
- Use an Apple ID with Account Holder, Admin, or App Manager permissions.

## App Record

- App name: `ClambHook`
- Bundle ID: `org.jpfchang.clambhook`
- SKU: `org.jpfchang.clambhook`
- Platform: iOS
- Price: Free
- Availability: `Specific Countries or Regions` -> `United States` only
- Support URL: `https://jpfchang.org/clambhook/support`
- Privacy Policy URL: `https://jpfchang.org/clambhook/privacy`

Do not select `All Countries or Regions` for v1. See `docs/app-store/territory-plan.md` before adding any future territory.

## In-App Purchases

Create exactly these two products. Do not create placeholders, subscriptions, consumables, or non-renewing subscriptions.

| Display name | Product ID | Type | US base price | Family Sharing |
| --- | --- | --- | --- | --- |
| ClambHook Lifetime Unlock | `org.jpfchang.clambhook.unlock.lifetime` | Non-Consumable | USD 99.99 | On |
| ClambHook 2027 Feature Update | `org.jpfchang.clambhook.feature_update.2027` | Non-Consumable | USD 8.99 | On |

Descriptions:

- `ClambHook Lifetime Unlock`: `Unlocks lifetime mobile access for ClambHook.`
- `ClambHook 2027 Feature Update`: `Unlocks ClambHook mobile features released in the 2027 update cycle.`

Important: App Store Connect product IDs cannot be reused after assignment. Family Sharing cannot be turned off after it is enabled for an In-App Purchase.

## Review Submission

- Attach both In-App Purchases to the first app version submission if they have not been approved before.
- Paste the pricing and In-App Purchase text from `docs/app-store/review-notes.md` into App Review Notes.
- Paste only the App Review demo profile credentials into App Store Connect. Do not commit them.
- Run `make app-review-release-check` with `CLAMBHOOK_APP_REVIEW_DEMO_PASSWORD` set before archiving.

## Verification

- Confirm StoreKit can fetch exactly:
  - `org.jpfchang.clambhook.unlock.lifetime`
  - `org.jpfchang.clambhook.feature_update.2027`
- Confirm sandbox purchase, restore, Family Sharing entitlement, and refund/revocation handling before release.
- Confirm the Settings/Purchases screen displays the product names and localized prices from App Store Connect.
