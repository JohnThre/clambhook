# GitHub CI/CD

GitHub Actions is the authoritative CI and release system. Ordinary CI keeps
read-only permissions and may upload short-lived reports. The protected
`.github/workflows/release.yml` workflow is the only workflow allowed to build
and publish end-user installers.

## Protected release environment

Create a `production` environment with required reviewers and deployment
branches limited to protected `master` and release tags. Keep workflow defaults
at `permissions: {}`; only release mutation jobs receive `contents: write`.

Configure these environment secrets:

- `GPG_PRIVATE_KEY_BASE64`, `GPG_PASSPHRASE`
- `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`,
  `ANDROID_KEY_ALIAS`, `ANDROID_KEY_PASSWORD`
- `APPLE_TEAM_ID`, `APPLE_DEVELOPER_ID_P12_BASE64`,
  `APPLE_DEVELOPER_ID_P12_PASSWORD`, `APPLE_KEYCHAIN_PASSWORD`
- `APPLE_ID`, `APPLE_APP_SPECIFIC_PASSWORD`, `APPLE_API_KEY_P8_BASE64`,
  `APPLE_API_KEY_ID`, `APPLE_API_ISSUER_ID`
- `SPARKLE_PRIVATE_KEY_BASE64`

Cloudflare credentials and R2 bucket variables are not used.

## Release behavior

A pushed `v*` tag must be an annotated GPG-signed tag made by the pinned
ClambHook release key. The workflow verifies the tag before the protected jobs
start, builds the Trisquel `.deb`, Rocky `.rpm`, Android 12+ APK, and macOS DMG,
then signs, notarizes where applicable, and uploads the allowlisted files to the
matching GitHub Release. Recovery runs replace only same-named assets.

Manual beta runs execute from `master`, create a versioned prerelease, and copy
its assets into the rolling `beta` prerelease. Stable clients use
`releases/latest/download/...`; beta clients use `releases/download/beta/...`.
Manifest and Sparkle enclosure URLs always point to the immutable versioned
release.

## Validation

Run before delivery:

```sh
scripts/check-github-actions.sh
shellcheck scripts/*.sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
```

The macOS test suite remains local-only; the protected workflow archives,
signs, notarizes, and publishes the locally validated product.
