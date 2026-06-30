# Privacy Notes

Last updated: 2026-06-30

ClambHook is distributed from `store.clambercloud.com`. It offers System Proxy
mode for apps that honor system proxy settings and daemon-backed Enhanced Mode
for device-wide routing according to the user's selected profiles and rules.

Profile data, proxy credentials, private keys, connection metadata, traffic
logs, diagnostics, and local captures stay on the device unless the user
explicitly exports or sends them. ClambHook does not install a certificate
authority, perform TLS man-in-the-middle inspection, store request or response
bodies, or provide body-level HAR capture in the public product posture.

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
