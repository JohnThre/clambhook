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

ClambHook uses a USD 99.99 one-time direct-sale license after a
one-calendar-month trial. It includes one year of all updates from the purchase
date, versions released on or before the update cutoff remain usable, it covers
a maximum of 10 concurrently active devices across supported platforms, and
license seats can be deactivated and moved to another device. A USD 9.99 renewal
buys one additional update year, extending from the later of the current cutoff
or the renewal payment date. Releases after the cutoff are not included,
including critical, bug, and security updates.

Creem and NOWPayments are the only ClambHook purchase payment providers; PayPal
is not accepted. Those providers process payment details;
`store.swiphtgroup.com` stores checkout IDs, order IDs, product IDs, license
state, and purchase timestamps needed to deliver and support the license.

Support contact: support@swiphtgroup.com
