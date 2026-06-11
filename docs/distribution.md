# Distribution Policy

ClambHook's end-user iPhone release is distributed only through the Apple App Store.

## End-user Downloads

- The App Store app price is Free.
- Premium access and paid feature updates are sold through non-consumable In-App Purchases.
- Family Sharing is enabled for premium In-App Purchases in App Store Connect.
- GitHub is source-only and view-only for end users.
- App Store Release builds require privacy-preserving server license validation using App Attest and StoreKit 2 transaction JWS values; see `docs/license-validation.md`.

The source is proprietary to Pengfan Chang, all rights reserved, and may not be copied, modified, built, run, contributed to, redistributed, packaged, released, hosted, sublicensed, or used to create derivative works without separate prior written permission from Pengfan Chang.

## In-App Purchase Products

| Display name | Product ID | Type | US base price |
| --- | --- | --- | --- |
| ClambHook Lifetime Unlock | `org.jpfchang.clambhook.unlock.lifetime` | Non-Consumable | USD 99.99 |
| ClambHook 2027 Feature Update | `org.jpfchang.clambhook.feature_update.2027` | Non-Consumable | USD 8.99 |

Future paid feature update products use `org.jpfchang.clambhook.feature_update.YYYY`.

The lifetime unlock includes features released through the purchase date plus one year. Each paid feature update extends that feature-release cutoff by one year. Existing purchased features remain enabled forever; only features released after the user's paid window require a USD 8.99 feature update. Bug fixes and security fixes remain included.

## GitHub Release Rule

Do not release end-user installers or package artifacts on GitHub. This includes `.dmg`, `.pkg`, `.apk`, `.aab`, `.ipa`, Homebrew formula releases, Debian packages, and macOS installer artifacts.

Non-iPhone build, package, and release targets remain available for Pengfan Chang's internal developer QA only. Only Pengfan Chang may distribute, publish, package, or release ClambHook source code or artifacts.
