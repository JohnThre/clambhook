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
   `scripts/sign-release-tag.sh v1.2.3 create`, and pin the apps repository's
   `core/` submodule to it.
2. Run `VERSION=1.2.3 UPDATE_CHANNEL=stable scripts/release-linux-local.sh`.
   It needs Podman or Docker; x86_64 builds run under emulation on Apple
   Silicon. It:
   - builds the core packages for each architecture in Ubuntu and Fedora
     containers (each build runs the installed-package smoke test);
   - builds the `clambhook-ui` packages through the apps repository's
     `scripts/release-linux-ui.sh`;
   - signs every package on the host (embedded `rpmsign`/`debsigs`-format and
     detached signatures, checksums, manifests). The private key never enters a
     container;
   - verifies every package with `debsig-verify`/`rpmkeys` in a key-free
     container and generates the apt/dnf metadata there;
   - signs the repository metadata on the host and bundles it as
     `clambhook-linux-repo-<version>.tar.gz`;
   - installs from the signed repositories on Ubuntu and Fedora. This proves
     that `clambhook-ui` pulls in `clambhook` and that tampered metadata is
     refused. Set `CLAMBHOOK_UPGRADE_FROM` to a directory of 1.0.x packages to
     also prove that an upgrade keeps the desktop controller.
3. Publish with `scripts/publish-release.sh --publish 1.2.3`. It runs
   `scripts/verify-release-signatures.sh` over every asset before uploading.
4. Deploy the `clambhook-linux-repo/` tree from the signed bundle to
   `https://clambercloud.com/clambhook/linux/`, after verifying its signature.
   Installed packages point apt/dnf at that location.

For a local preflight without release builds, run:

```sh
scripts/validate-linux-distros.sh ubuntu fedora
```

Before publishing, verify both architectures, package metadata, daemon
integration, clean uninstall, SHA-256 files, embedded and detached GPG
signatures, signed repository metadata, and the immutable GitHub URLs in each
manifest.
