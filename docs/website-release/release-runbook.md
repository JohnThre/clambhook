<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# macOS and Android release runbook

GitHub Releases on this repository is the sole public artifact host, and the
in-app updaters read it. The macOS and Android installers are built in the
private apps repository on the maintainer's Mac, then published from here with
`scripts/publish-release.sh`, which verifies every signature before uploading.

Create an annotated signed stable tag in this repository first:

```sh
scripts/sign-release-tag.sh v1.2.3 create
git push origin v1.2.3
```

Then pin the apps repository's `core/` submodule to that tag.

For macOS, run `make test-apple`, `make build-apple`, and `make release-macos`
in the apps repository. The release script archives with Developer ID,
notarizes and staples the app and DMG, creates SHA-256 and GPG signatures, and
signs the Sparkle appcast with the pinned EdDSA key.

For Android, run `make test-android` and `make release-android` in the apps
repository. Gradle builds the shared Kotlin/Compose application with the
release keystore, produces ARM64 APK and AAB files, verifies both signatures
and ABI contents, and produces GPG-signed checksums and an update manifest. The
application ID remains `org.jpfchang.clambhook`, the minimum remains Android
12/API 31, and the target remains API 36.

Run `scripts/publish-release.sh 1.2.3` (dry run), then
`scripts/publish-release.sh --publish 1.2.3`. Afterwards, download every
release asset, verify checksums and signatures, confirm macOS notarization and
the APK/AAB signatures, and exercise stable update discovery. Do not announce a
release until every selected platform's assets are uploaded. Use `--beta` for a
beta prerelease; beta assets are also mirrored to the rolling `beta`
prerelease.
