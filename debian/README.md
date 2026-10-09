<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Debian Packaging

This directory is the Debian source-package recipe for the official Ubuntu
24.04 LTS release lane. It installs the GPL-3.0-only core: the C17 daemon and
license helper together with systemd, sysusers, tmpfiles, license, notice,
repository and sample-configuration resources. The desktop controller and TUI
are the separate proprietary `clambhook-ui` package, built from the private
apps repository; `clambhook` recommends it.

Run the recipe through `scripts/ci-linux-package-recipes.sh debian` or the full
`scripts/validate-linux-distros.sh ubuntu` harness. The harness builds and
installs the package, starts the daemon, inspects the payload, and verifies
clean uninstall.

Official x86_64 and aarch64 `.deb` files, checksums, signatures and manifests
are built and signed on the maintainer's machine and published to GitHub
Releases with `scripts/publish-release.sh`. Other local package builds are
validation artifacts and must not be presented as official releases.
