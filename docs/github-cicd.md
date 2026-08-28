# GitHub CI/CD

GitHub Actions is ClambHook's primary continuous-integration and release
orchestrator. GitHub remains source-only: test reports may be retained briefly
as Actions artifacts, but end-user installers and packages are never attached
to a GitHub Release or uploaded as Actions artifacts. Protected release jobs
publish directly to the private Cloudflare R2 distribution bucket used by
`store.clambercloud.com`.

## Workflows

| Workflow | Trigger | Responsibility |
| --- | --- | --- |
| `ci.yml` | `master`, pull requests, tags, manual | Source/workflow policy, C sanitizers and macOS portability, Android unit/lint/build, API 31/33/36 Compose/JNI tests, and only Trisquel 12, Rocky Linux 9, and AlmaLinux 9 for GNU/Linux; the macOS app is tested locally |
| `security.yml` | `master`, pull requests, weekly, manual | CodeQL for C/C++, Go, Kotlin/Java, and Swift; dependency review |
| `release.yml` | signed `v*` tag or manual | Protected signing, notarization, checksums/manifests, and direct R2 deployment for Android, GNU/Linux, and macOS |
| `dependabot.yml` | GitHub schedule | GitHub Actions, Go modules, Android/Linux Gradle, and Swift dependency updates |

Swift CodeQL traces only the shared Swift package target. It does not build or
test `ClambhookMac`; macOS app builds and tests remain local. Go CodeQL uses a
manual legacy-source build until the final C cutover removes the Go tree and
its matrix row. `scripts/check-github-actions.sh` rejects `test-apple` and
`swift test` in every workflow. It also rejects `build-apple` or a direct
`ClambhookMac` Xcode build outside the protected publication workflow; that
workflow may archive, sign, and notarize the already locally tested app, but it
may not run the app test suite.

All workflows default to `permissions: {}`. Jobs grant only `contents: read` or
the security permissions they require. Third-party actions are pinned to full
commit SHAs. `scripts/check-github-actions.sh` and
`scripts/check-source-only.sh` fail if those rules regress or a workflow tries
to publish an installer through GitHub.

The GNU/Linux test matrix is intentionally limited to exactly:

- Trisquel 12 Ecne
- Rocky Linux 9
- AlmaLinux 9

Rocky and Alma use their official OCI images. On x86-64 GitHub runners,
Trisquel is bootstrapped from its official `ecne` package archive after the
archive key fingerprint is checked. On ARM64 local hosts the same harness can
use Trisquel's official SHA-256-pinned base root filesystem. Release `.deb`
packages are produced in Trisquel; release `.rpm` packages are produced in
Rocky. Alma is a test lane and does not produce a duplicate RPM.

## One-time GitHub setup

Create a GitHub environment named `production` and configure:

- required reviewers;
- deployment branches/tags limited to protected `master` and release tags;
- no bypass for unreviewed deployments;
- environment variable `CLAMBHOOK_R2_BUCKET` (default:
  `clambhook-artifacts`);
- the environment secrets listed below.

Repository branch protection for `master` should require the applicable CI and
security checks, require review, dismiss stale approvals, and prohibit force
pushes. Do not grant workflow write permissions globally; the checked-in jobs
declare their own least-privilege permissions.

### Production environment secrets

Shared signing and publication:

- `CLOUDFLARE_ACCOUNT_ID`
- `CLOUDFLARE_API_TOKEN` scoped only to the ClambHook R2 bucket
- `GPG_PRIVATE_KEY_BASE64`
- `GPG_PASSPHRASE`

Android:

- `ANDROID_KEYSTORE_BASE64`
- `ANDROID_KEYSTORE_PASSWORD`
- `ANDROID_KEY_ALIAS`
- `ANDROID_KEY_PASSWORD`

Apple:

- `APPLE_TEAM_ID`
- `APPLE_DEVELOPER_ID_P12_BASE64`
- `APPLE_DEVELOPER_ID_P12_PASSWORD`
- `APPLE_KEYCHAIN_PASSWORD`
- `APPLE_ID`
- `APPLE_APP_SPECIFIC_PASSWORD`
- `APPLE_API_KEY_P8_BASE64`
- `APPLE_API_KEY_ID`
- `APPLE_API_ISSUER_ID`
- `SPARKLE_PRIVATE_KEY_BASE64`

The ordinary CI workflow does not build or test the macOS app. Run
`make test-apple` and `make build-apple` on the developer Mac before release;
the protected release workflow builds the signed/notarized artifact only when
publishing an approved release.

Encode binary/key files as one-line base64 values, for example:

```sh
openssl base64 -A -in /secure/path/to/file
```

Never commit a private key, keystore, password, token, or generated installer.
The public release key is intentionally tracked at
`keys/clambhook-release-key.asc`; the workflow pins its full primary
fingerprint `BAFC7769FDA1E0D4EBD23E2F6FF4807EAD977A9B` and signing-subkey
fingerprint `F09990BBE647C2D43F58D6F0EAA876B70B1832F5`.

## Release behavior

A pushed `v*` tag must be an annotated GPG-signed tag made by the pinned
ClambHook release key. The unprivileged `prepare` job authenticates the tag
with the tracked public key before any protected job can start. Tag releases
always publish all platforms to the stable channel.

Manual dispatch is the recovery/beta path. It is accepted only when the
workflow runs from `master`, requires a semantic version, and still passes the
`production` environment approval. The reviewer selects `beta` or `stable` and
all platforms or one platform.

Protected jobs minimize credential exposure:

- Trisquel/Rocky build unsigned packages in isolated containers. The GPG
  private key is imported only after the builds; checksums and the combined
  manifest are then signed on the protected runner.
- Android builds without the Android signing key. The trusted SDK `apksigner`
  receives the temporary keystore only after the build, verifies the signed
  package, and deletes the keystore before metadata signing and R2 upload.
- macOS imports a Developer ID certificate into a temporary keychain, archives
  with Xcode, notarizes and staples the app and DMG, creates a Sparkle EdDSA
  appcast, and removes the temporary keychain and key files.
- Cloudflare credentials are exposed only to each final upload step. No
  installer or package is copied into GitHub's artifact store.

The workflow does not edit website environment variables. The R2 keys are
stable/versioned paths already consumed by the documented
`store.clambercloud.com` download and update routes.

## Local validation

Run the same static workflow gates before committing:

```sh
scripts/check-github-actions.sh
shellcheck scripts/*.sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
```

For platform validation, see [`release-validation.md`](release-validation.md).
For signing and owner QA, see the Android/macOS release runbook and
[`website-release/linux-release-runbook.md`](website-release/linux-release-runbook.md).
