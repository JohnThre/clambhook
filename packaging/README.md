<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Packaging

GNU/Linux ships as two packages in the same signed apt and dnf repositories.
Ubuntu and Fedora are the only supported distributions.

| Package | License | Built from | Contents |
| --- | --- | --- | --- |
| `clambhook` | GPL-3.0-only (+ Apache-2.0 `clib`) | this repository | C17 daemon, license helper, systemd unit, sysusers/tmpfiles, sample config, repository configuration, licenses and notices |
| `clambhook-ui` | proprietary | private apps repository | Kotlin/Compose desktop controller with a private jlink runtime, `clambhook-tui`, desktop entry, AppStream metadata, icon, polkit policy |

`clambhook` recommends `clambhook-ui`, and `clambhook-ui` depends on the
matching `clambhook` version and replaces the combined 1.0.x package. So an
upgrade from 1.0.x keeps the desktop controller, and a server can install the
core alone with `--no-install-recommends` (apt) or
`--setopt=install_weak_deps=False` (dnf). Android packages and the macOS app
are built in the apps repository: the APK compiles this core in through JNI,
and the macOS app embeds the C daemon.

## Core payload

The Debian and RPM recipes here install:

- `/usr/bin/clambhook`
- `/usr/bin/clambhook-license`
- `clambhook-daemon.service`, sysusers/tmpfiles metadata and the sample config
- the signed repository configuration: on Ubuntu
  `/etc/apt/sources.list.d/clambhook.sources` and
  `/usr/share/keyrings/clambhook-archive-keyring.asc`; on Fedora
  `/etc/yum.repos.d/clambhook.repo` and
  `/etc/pki/rpm-gpg/RPM-GPG-KEY-clambhook`
- licenses, notices, and documentation

Package inspection rejects client payloads (`clambhook-ui`, `clambhook-tui`,
desktop integration, Java runtimes), retired JavaFX/Gluon/GTK payloads, and
runtime build metadata from the retired implementation.

## Authoritative distro matrix

| Distribution | Architectures | Role |
| --- | --- | --- |
| Ubuntu 24.04 LTS | x86_64, aarch64 | Build/test Debian package |
| Fedora Linux 44 | x86_64, aarch64 | Build/test RPM package |

CI uses the official container images on native-architecture GitHub runners.
Run the matrix locally with Podman or Docker:

```sh
scripts/validate-linux-distros.sh
scripts/validate-linux-distros.sh ubuntu
scripts/validate-linux-distros.sh fedora
```

## Recipe validation

`scripts/ci-linux-package-recipes.sh debian` builds the Debian recipe inside
Ubuntu. `scripts/ci-linux-package-recipes.sh rpm` builds the RPM recipe inside
Fedora. `scripts/package-smoke.sh` validates metadata, production binary names,
daemon unit hardening, the license helper contract, the expected architecture,
the absence of client payload, and clean uninstall behavior.

## Release files

Releases are built and signed on the maintainer's machine. For each GNU/Linux
architecture the release contains:

- `ClambHook-<version>-<arch>.deb` and `.rpm` (core) with embedded
  `debsigs`/RPM header signatures
- the matching `clambhook-ui` packages from the apps repository
- `clambhook-linux-<arch>-manifest.json`
- SHA-256 files and armored detached GPG signatures (`.sig`) for every package,
  checksum, and manifest

`scripts/build-linux-repos.sh` builds one signed apt and dnf repository
(`InRelease`/`Release.gpg`, `repomd.xml.asc`) containing both packages.
`scripts/test-linux-repo-install.sh` installs from it in Ubuntu 24.04 and
Fedora 44 containers with signature checking enabled and proves that tampered
metadata is refused. The repository tree is published as
`clambhook-linux-repo-<version>.tar.gz` (signed) and deployed to
`https://jpfchang.org/clambhook/linux/`. All signatures use the
developer@jpfchang.org key; see
[release signing](../docs/website-release/signing.md).

The Android release (`ClambHook-arm64.apk`/`.aab`) and the macOS release (the
notarized Apple Silicon DMG and ZIP, update manifest and Sparkle appcast) come
from the apps repository with the same signing rules.

`scripts/publish-release.sh` runs `scripts/verify-release-signatures.sh` over
every asset before it uploads anything to GitHub Releases. No CI workflow
builds, signs, or publishes installers. The asset list is a release contract,
not evidence that the GitHub Release currently exists.

See [release validation](../docs/release-validation.md) and
[distribution policy](../docs/distribution.md).
