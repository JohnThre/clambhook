<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# macOS and Android release runbook

GitHub Releases is the sole public artifact host. After request validation, each
protected platform job imports only its required credentials into temporary
files, builds and inspects signed assets, and uploads them directly to the
versioned release.

For macOS, run `make test-apple` and `make build-apple` locally before tagging.
The workflow then archives with Developer ID, notarizes and staples the app and
DMG, creates SHA-256 and GPG signatures, and signs the Sparkle appcast with the
pinned EdDSA key.

For Android, Gluon builds the shared JavaFX application with the protected
keystore, produces ARM64 APK and AAB files, verifies both signatures and ABI
contents, and produces GPG-signed checksums and an update manifest. The
application ID remains `org.jpfchang.clambhook`, the minimum remains Android
12/API 31, and the target remains API 36.

Create an annotated signed stable tag with:

```sh
scripts/sign-release-tag.sh v1.2.3 create
git push origin v1.2.3
```

After approval, download every release asset, verify checksums/signatures,
confirm macOS notarization and APK/AAB signatures, and exercise stable update
discovery. Do not announce the release while selected platform jobs are still
running or failed. Use manual dispatch for beta or an idempotent recovery
upload; approved beta assets are mirrored to the rolling `beta` prerelease.
