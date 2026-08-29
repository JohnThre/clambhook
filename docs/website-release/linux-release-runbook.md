<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# GNU/Linux release runbook

The protected GitHub workflow builds `.deb` packages in Ubuntu 24.04 LTS and
`.rpm` packages in Fedora Linux 44 on x86_64 and aarch64. These are the only
authoritative GNU/Linux test targets. Each package, checksum, checksum
signature, and `clambhook-linux-<arch>-manifest.json` is uploaded to the
versioned GitHub Release. Every package contains the C17 command-line programs
and self-contained JavaFX/Gluon native image with no bundled JRE.

For local preflight, run `scripts/validate-linux-distros.sh`, then build with:

```sh
VERSION=1.2.3 RELEASE_TAG=v1.2.3 REQUIRE_SIGNING=1 make release-linux
```

Before publishing, verify both architectures, package metadata, daemon and
secret-storage integration, JavaFX launch, clean uninstall, SHA-256 files, GPG
signatures, and the immutable GitHub URLs in each manifest. Create and
push the signed tag with `scripts/sign-release-tag.sh v1.2.3 create`; GitHub
Actions performs publication after production approval.
