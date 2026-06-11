# License Validation

The iOS App Store build uses server-backed license validation for
server-controlled free access and premium purchases. Local StoreKit verification
remains useful for UI state, but Release builds must receive a server grant
before free access is considered active.

## Production Backend

The production license backend is hosted by `jpfchang.org` under:

`https://jpfchang.org/clambhook/license`

This repository no longer contains a standalone Go license server. Backend
deployment, persistent storage, backups, rate limiting, Apple trust roots,
DeviceCheck credentials, monitoring, and log redaction are maintained in the
`jpfchang.org` production infrastructure.

The app stores and transmits stable identifiers only through the hosted license
flow. The backend stores keyed hashes of app install IDs, App Attest key IDs,
and transaction IDs. It does not store user accounts, email addresses, profile
data, traffic data, or raw stable device identifiers for ClambHook licensing.

## Endpoints

- `POST /clambhook/license/v1/license/challenge` returns a one-time challenge
  for attestation or assertion validation.
- `POST /clambhook/license/v1/license/attest` validates an App Attest object
  and starts server-controlled free access when policy allows it.
- `POST /clambhook/license/v1/license/validate` validates an App Attest
  assertion plus StoreKit 2 transaction JWS values and returns a signed license
  grant.

The iOS app sends StoreKit 2 `jwsRepresentation` values, not legacy App Store
receipts.
