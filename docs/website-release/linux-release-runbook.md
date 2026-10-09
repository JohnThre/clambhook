<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# GNU/Linux release runbook

The protected GitHub workflow builds `.deb` packages in Ubuntu 24.04 LTS and
`.rpm` packages in Fedora Linux 44 on x86_64 and aarch64. These are the only
authoritative and only supported GNU/Linux targets. After the container
builds, the protected runner embeds an RPM header signature or `debsigs`
origin signature in each package, then writes detached developer@jpfchang.org
signatures for the package, its checksum, and
`clambhook-linux-<arch>-manifest.json`. `scripts/verify-release-signatures.sh`
must pass before anything is uploaded to the versioned GitHub Release. The
`linux-repo` job then builds signed apt and dnf repositories, installs from
them in Ubuntu and Fedora containers, and publishes
`clambhook-linux-repo-<version>.tar.gz`. Deploy that bundle's
`clambhook-linux-repo/` tree to `https://jpfchang.org/clambhook/linux/` after
verifying its signature; installed packages point apt/dnf at that location. Every package contains the C17 command-line programs
and the self-contained Kotlin/Compose desktop controller with its private runtime.

For local preflight, run the same container matrix without publishing:

```sh
scripts/validate-linux-distros.sh ubuntu fedora
```

`make release-linux` is a local artifact/signing helper for a suitably
provisioned GNU/Linux host; it does not publish and does not replace the
protected two-architecture workflow.

Before publishing, verify both architectures, package metadata, daemon and
secret-storage integration, desktop controller launch, clean uninstall, SHA-256 files,
embedded and detached GPG signatures, signed repository metadata, and the immutable GitHub URLs in each manifest. Create and
push the signed tag with `scripts/sign-release-tag.sh v1.2.3 create`; GitHub
Actions performs publication after production approval.
