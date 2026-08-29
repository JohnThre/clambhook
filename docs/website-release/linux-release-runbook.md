# GNU/Linux release runbook

The protected GitHub workflow builds `.deb` packages in Trisquel 12 and `.rpm`
packages in Rocky Linux 9; AlmaLinux 9 remains validation-only. Each package,
checksum, checksum signature, and `clambhook-linux-manifest.json` is uploaded to
the versioned GitHub Release.

For local preflight, run `scripts/validate-linux-distros.sh`, then build with:

```sh
VERSION=1.2.3 RELEASE_TAG=v1.2.3 REQUIRE_SIGNING=1 make release-linux
```

Before publishing, verify package metadata, install/uninstall behavior, SHA-256
files, GPG signatures, and the immutable GitHub URLs in the manifest. Create and
push the signed tag with `scripts/sign-release-tag.sh v1.2.3 create`; GitHub
Actions performs publication after production approval.
