# macOS and Android release runbook

GitHub Releases is the sole public artifact host. The protected release workflow
imports credentials only after compilation, produces signed assets, and uploads
them directly to the versioned release.

For macOS, run `make test-apple` and `make build-apple` locally before tagging.
The workflow then archives with Developer ID, notarizes and staples the app and
DMG, creates SHA-256 and GPG signatures, and signs the Sparkle appcast with the
pinned EdDSA key.

For Android, the workflow builds without the signing key, signs the completed
APK with `apksigner`, verifies it, and produces a GPG-signed checksum and update
manifest. The manifest minimum remains Android 12/API 31.

Create an annotated signed stable tag with:

```sh
scripts/sign-release-tag.sh v1.2.3 create
git push origin v1.2.3
```

After approval, download every release asset, verify checksums/signatures,
confirm macOS notarization and APK signatures, and exercise stable update
discovery. Use manual dispatch for beta or an idempotent recovery upload.
