<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Debian Packaging

This directory is the Debian source-package recipe for the official Ubuntu
24.04 LTS release lane. It installs the C17 daemon, TUI, license helper, and
self-contained JavaFX/Gluon native image together with systemd, polkit,
sysusers, tmpfiles, desktop, AppStream, license, notice, and sample-configuration
resources. No JRE is bundled.

Run the recipe through `scripts/ci-linux-package-recipes.sh debian` or the full
`scripts/validate-linux-distros.sh ubuntu` harness. The harness builds and
installs the package, launches the daemon and JavaFX controller, exercises an
ephemeral Secret Service, inspects the payload, and verifies clean uninstall.

The protected release workflow publishes the resulting x86_64 and aarch64
`.deb` files, checksums, signatures, and per-architecture manifest to GitHub
Releases. Local package builds are validation artifacts and must not be
presented as official releases.
