# Privacy Notes

Last updated: 2026-06-30

ClambHook is distributed from `store.clambercloud.com`. It offers System Proxy
mode for apps that honor system proxy settings and daemon-backed Enhanced Mode
for device-wide routing according to the user's selected profiles and rules.

Profile data, proxy credentials, private keys, connection metadata, traffic
logs, diagnostics, and local captures stay on the device unless the user
explicitly exports or sends them. Activity inspection is metadata-only by
default. HTTP Capture is a separate local opt-in for traffic routed through the
daemon HTTP proxy; HTTPS capture requires a user-trusted local certificate
authority and can store bounded request and response body previews plus HAR
exports on this Mac.

When a user activates a direct-sale license, `store.swiphtgroup.com` receives
the license key, generated install ID, device display name, platform,
architecture, app version, activation state, and timestamps needed for device
seat management. License keys are hashed before storage. Profile contents are
not uploaded for license activation.

ClambHook uses a USD 99.99 direct-sale license: it includes one year of feature
updates, versions released during that year remain usable, it covers up to 10 active
devices across supported platforms, and license seats can be deactivated and
moved to another device. Optional USD 9.99 paid feature updates extend the
feature-update window by one year from the renewal purchase date.

Payment providers process payment details; `store.swiphtgroup.com` stores
checkout IDs, order IDs, product IDs, license state, and purchase timestamps
needed to deliver and support the license.

Support contact: support@swiphtgroup.com
