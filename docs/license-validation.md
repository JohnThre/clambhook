# License Validation

ClambHook uses the hosted `store.swiphtgroup.com` license backend for trial,
activation, device-seat, and update-year-renewal state. Public downloads and
update manifests are served from `store.clambercloud.com`.

## Production Backend

The production license backend is hosted under:

`https://store.swiphtgroup.com/clambhook/license/v1/devices`

This repository does not contain the hosted license server. Backend deployment,
persistent storage, backups, rate limiting, payment webhooks, email delivery,
monitoring, and log redaction are maintained in the `swiphtgroup.com` store
infrastructure.

The application stores and transmits stable identifiers only through the hosted
license flow. The backend stores hashed license keys, checkout records, license
transactions, entitlement windows, generated install IDs, device display names,
platform and architecture values, app version values, activation state, and
transfer/deactivation events needed to support the direct-sale license. Profile
contents, traffic data, proxy credentials, and private keys are not uploaded for
license activation.

## Endpoints

- `POST /clambhook/license/v1/devices/activate` activates or refreshes a licensed device.
- `POST /clambhook/license/v1/devices/deactivate` deactivates a device seat before transfer or retirement.
- `POST /clambhook/license/v1/devices/reactivate` reactivates a known device when policy allows it.
- `POST /clambhook/license/v1/devices/transfer` records a transfer by deactivating the current device seat.
- Compatibility aliases under `/clambhook/license/v1/macos/*` may remain available for older macOS builds during migration.

Website checkout and claim flows are exposed through `/api/clambhook/checkout`.
ClambHook purchase payments are accepted only through Creem or NOWPayments, not
PayPal, and license transactions must originate from verified provider webhook
events in the `swiphtgroup.com` store.

Users can manage device seats from
`https://store.swiphtgroup.com/clambhook/portal/`.

## Distribution Contract

A USD 99.99 one-time ClambHook license is required after the one-calendar-month
trial and includes one year of all updates from the purchase date. Versions
released on or before the update cutoff remain usable; each license covers a
maximum of 10 concurrently active devices across supported platforms. Device
seats can be deactivated and moved to another device. Each USD 9.99 renewal buys
one additional update year, extending from the later of the current cutoff or
the renewal payment date. Releases after the cutoff are not included, including
critical, bug, and security updates. Public installers are downloaded from
`store.clambercloud.com`, and generated installer artifacts must not be
published from GitHub or package mirrors.
