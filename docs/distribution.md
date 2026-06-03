# Distribution Policy

ClambHook's end-user iPhone release is distributed only through the Apple App Store.

## End-user Downloads

- The App Store app price is Free.
- Premium access is sold through non-consumable In-App Purchases.
- Family Sharing is enabled for premium In-App Purchases in App Store Connect.
- GitHub is source-only for end users.

## In-App Purchase Products

| Display name | Product ID | Type | US base price |
| --- | --- | --- | --- |
| ClambHook Lifetime Unlock | `org.jpfchang.clambhook.unlock.lifetime` | Non-Consumable | USD 99.99 |
| ClambHook 2027 Feature Update | `org.jpfchang.clambhook.feature_update.2027` | Non-Consumable | USD 8.99 |

Future paid feature update products use `org.jpfchang.clambhook.feature_update.YYYY`.

## GitHub Release Rule

Do not release end-user installers or package artifacts on GitHub. This includes `.dmg`, `.pkg`, `.apk`, `.aab`, `.ipa`, Homebrew formula releases, Debian packages, and macOS installer artifacts.

Non-iPhone build, package, and release targets remain available for internal developer QA only.
