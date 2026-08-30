<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Privacy Notes

Last updated: 2026-06-30

ClambHook is distributed through GitHub Releases and described on `clambercloud.com`. It offers System Proxy
mode for apps that honor system proxy settings and daemon-backed Enhanced Mode
for device-wide routing according to the user's selected profiles and rules.

Profile data, proxy credentials, private keys, connection metadata, traffic
logs, diagnostics, and local captures stay on the device unless the user
explicitly exports or sends them. Activity inspection is metadata-only by
default. HTTP Capture is a separate local opt-in for traffic routed through the
daemon HTTP proxy; HTTPS capture requires a user-trusted local certificate
authority and can store bounded request and response body previews plus HAR
exports on this Mac.

When a user activates a provider-neutral license, `store.swiphtgroup.com` receives
the license key, generated install ID, device display name, platform,
architecture, app version, activation state, and timestamps needed for device
seat management. License keys are hashed before storage. Profile contents are
not uploaded for license activation.

New installations receive a 7-day trial; already-started legacy trials keep
their original month-long end date. A recurring USD 79.99 annual subscription
covers releases published during each paid term and a maximum of 6 concurrently
active devices. License seats can be deactivated and moved. Cancellation stops
future billing at the paid-through date, while compatible versions released
during paid terms remain usable perpetually.

Creem and NOWPayments are the only ClambHook purchase payment providers; PayPal
is not accepted. Those providers process the required delivery email and payment details;
`store.swiphtgroup.com` stores checkout IDs, order IDs, product IDs, license
state, subscription status, paid-period dates, and purchase timestamps needed
to deliver and support the license. Donation services are separate and never
create licenses, extend subscriptions, or grant supporter badges.

Support contact: support@swiphtgroup.com
