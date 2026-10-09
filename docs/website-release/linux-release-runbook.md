<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# GNU/Linux release runbook

GNU/Linux releases ship two packages per format and architecture: `clambhook`
(the GPL-3.0-only core, built here) and `clambhook-ui` (the proprietary desktop
controller and TUI, built in the private apps repository). Both are built on
the maintainer's machine in Ubuntu 24.04 LTS (`.deb`) and Fedora Linux 44
(`.rpm`) containers for x86_64 and aarch64. These are the only authoritative
and only supported GNU/Linux targets.

After the container builds, each package gets an embedded RPM header signature
or `debsigs` origin signature. Detached developer@jpfchang.org signatures are
then written for the package, its checksum, and
`clambhook-linux-<arch>-manifest.json`.

1. Create and push the signed tag with
   `scripts/sign-release-tag.sh v1.2.3 create`.
2. Build and sign the core packages:
   `CLAMBHOOK_LINUX_RELEASE_BUILD=1 VERSION=1.2.3 scripts/validate-linux-distros.sh ubuntu fedora`,
   then `scripts/sign-linux-release-artifacts.sh`. Each container build also
   runs the installed-package smoke test.
3. Build and sign the `clambhook-ui` packages in the apps repository.
4. Build the signed apt and dnf repositories from both sets of packages with
   `scripts/build-linux-repos.sh`, and install-test them in Ubuntu and Fedora
   containers with `scripts/test-linux-repo-install.sh`. This proves that
   installing `clambhook-ui` pulls in `clambhook`, that an upgrade from 1.0.x
   keeps the desktop controller, and that tampered metadata is refused.
5. Publish with `scripts/publish-release.sh --publish 1.2.3`. It runs
   `scripts/verify-release-signatures.sh` over every asset before uploading.
6. Deploy the `clambhook-linux-repo/` tree from the signed
   `clambhook-linux-repo-<version>.tar.gz` to
   `https://jpfchang.org/clambhook/linux/`, after verifying its signature.
   Installed packages point apt/dnf at that location.

For a local preflight without release builds, run:

```sh
scripts/validate-linux-distros.sh ubuntu fedora
```

Before publishing, verify both architectures, package metadata, daemon
integration, clean uninstall, SHA-256 files, embedded and detached GPG
signatures, signed repository metadata, and the immutable GitHub URLs in each
manifest.
