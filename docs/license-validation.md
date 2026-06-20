# License Validation

The macOS direct-sale build uses the hosted jpfchang.org license backend for
trial, activation, device-seat, and paid-feature-update state. The current
public distribution path is the website download for Apple Silicon Macs.

## Production Backend

The production macOS license backend is hosted by `jpfchang.org` under:

`https://jpfchang.org/clambhook/license/v1/macos`

This repository no longer contains a standalone Go license server. Backend
deployment, persistent storage, backups, rate limiting, payment webhooks,
email delivery, monitoring, and log redaction are maintained in the
`jpfchang.org` production infrastructure.

The app stores and transmits stable identifiers only through the hosted license
flow. The backend stores hashed license keys, checkout records, license
transactions, entitlement windows, generated install IDs, device display names,
platform and architecture values, app version values, activation state, and
transfer/deactivation events needed to support the direct-sale license. Profile
contents, traffic data, proxy credentials, and private keys are not uploaded for
license activation.

## Endpoints

- `POST /clambhook/license/v1/macos/activate` activates or refreshes a licensed
  Mac seat.
- `POST /clambhook/license/v1/macos/deactivate` deactivates a Mac seat before
  transfer or retirement.
- `POST /clambhook/license/v1/macos/reactivate` reactivates a known Mac seat
  when policy allows it.
- `POST /clambhook/license/v1/macos/transfer` records a transfer between Mac
  seats.

Website checkout and claim flows are exposed through `/api/clambhook/checkout`,
`/api/clambhook/claim`, `/api/clambhook/nowpayments-webhook`, and the shared
Creem webhook handler in the jpfchang.org site.

## Distribution Contract

Direct-sale ClambHook licenses are valid only for macOS on Apple Silicon. The
public installer is downloaded from jpfchang.org, and generated installer
artifacts must not be published from GitHub or package mirrors.
