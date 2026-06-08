# License Validation

The iOS App Store build uses server-backed license validation for trials and
premium purchases. Local StoreKit verification remains useful for UI state, but
Release builds must receive a server grant before the trial is considered
active.

## Server

Build the validator with:

```sh
make build-license-server
```

Run it with a private store, Apple trust roots, DeviceCheck credentials, and
32-byte or longer secrets:

```sh
CLAMBHOOK_LICENSE_APP_ID="TEAMID.org.jpfchang.clambhook" \
CLAMBHOOK_LICENSE_HMAC_SECRET="replace-with-32-byte-secret" \
CLAMBHOOK_LICENSE_GRANT_SECRET="replace-with-32-byte-secret" \
CLAMBHOOK_LICENSE_APPLE_ROOTS_PEM="$(cat AppleRootCA.pem)" \
CLAMBHOOK_LICENSE_DEVICECHECK_TEAM_ID="TEAMID" \
CLAMBHOOK_LICENSE_DEVICECHECK_KEY_ID="KEYID" \
CLAMBHOOK_LICENSE_DEVICECHECK_PRIVATE_KEY_PEM="$(cat AuthKey_KEYID.p8)" \
bin/clambhook-license-server -addr 127.0.0.1:9091 -store /var/lib/clambhook/license-store.json
```

For development-only smoke tests without Apple receipt refresh, pass
`-allow-no-receipt-risk` and use `-environment development`.

App Attest receipt refresh and Apple fraud metrics use:

- `CLAMBHOOK_LICENSE_DEVICECHECK_TEAM_ID`
- `CLAMBHOOK_LICENSE_DEVICECHECK_KEY_ID`
- `CLAMBHOOK_LICENSE_DEVICECHECK_PRIVATE_KEY_PEM`
- `CLAMBHOOK_LICENSE_APP_ATTEST_RECEIPT_URL` for development, usually `https://data-development.appattest.apple.com/v1/attestationData`

The server stores HMACs of app install IDs, App Attest key IDs, and transaction
IDs. It does not store user accounts, email addresses, profile data, traffic
data, or raw stable device identifiers.

## Endpoints

- `POST /v1/license/challenge` returns a one-time challenge for attestation or
  assertion validation.
- `POST /v1/license/attest` validates an App Attest object and starts the server
  trial when policy allows it.
- `POST /v1/license/validate` validates an App Attest assertion plus StoreKit 2
  transaction JWS values and returns a signed license grant.

The iOS app sends StoreKit 2 `jwsRepresentation` values, not legacy App Store
receipts.
