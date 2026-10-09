<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Release signing

ClambHook GitHub Releases use three independent trust layers:

- GPG signs **every installer** and its checksum, every update manifest, and
  the apt/dnf repository metadata with the developer@jpfchang.org key pinned in
  `keys/clambhook-release-key.asc` (primary fingerprint
  `BAFC 7769 FDA1 E0D4 EBD2 3E2F 6FF4 807E AD97 7A9B`, signing subkey
  `F099 90BB E647 C2D4 3F58 D6F0 EAA8 76B7 0B18 32F5`).
- Apple Developer ID, notarization, stapling, and Sparkle EdDSA protect macOS.
- The stable Android keystore signs APK and AAB files; `apksigner verify`
  and `jarsigner -verify -strict` run before upload.

| Installer | GPG signatures |
| --- | --- |
| Ubuntu `.deb` | embedded `debsigs` origin signature, detached `.deb.sig`, signed `.sha256`, signed apt `InRelease`/`Release.gpg` |
| Fedora `.rpm` | embedded RPM header signature, detached `.rpm.sig`, signed `.sha256`, signed dnf `repomd.xml.asc` |
| Android `.apk`/`.aab` | detached `.sig`, signed `.sha256`, signed update manifest |
| macOS `.dmg`/`.zip` | detached `.sig`, signed `.sha256`, signed update manifest |

All signing goes through `scripts/lib/release-gpg.sh`. Before any asset is
uploaded, `scripts/verify-release-signatures.sh` re-verifies every installer,
checksum, and manifest against a scratch keyring holding only the pinned public
key, requires the pinned signing subkey, re-checks embedded package signatures,
and fails if the signing subkey expires within 30 days.
`scripts/check-github-actions.sh` rejects a release workflow in which an upload
is not preceded by that gate. The `linux-repo` release job also installs
ClambHook from the freshly signed repositories in Ubuntu 24.04 and Fedora 44
containers with signature checking enabled, and proves that tampered metadata
is refused.

The signing subkey currently expires on **2027-05-16**. Extend it (or add a
replacement subkey) and republish `keys/clambhook-release-key.asc` before then.

Private material is provided only through the protected `production`
environment, decoded into mode-0600 temporary files, and removed after use.
Never commit signing keys or put their values on compiler command lines.

## Verifying downloads

Import the public key from the same immutable versioned release:

```sh
gpg --import clambhook-release-key.asc
gpg --verify ClambHook-arm64.apk.sig ClambHook-arm64.apk
gpg --verify ClambHook-arm64.apk.sha256.sig ClambHook-arm64.apk.sha256
sha256sum -c ClambHook-arm64.apk.sha256
```

The same `gpg --verify <installer>.sig <installer>` check applies to every
`.deb`, `.rpm`, `.apk`, `.aab`, `.dmg`, and `.zip`. Embedded package signatures
can be checked as well:

```sh
# Fedora
sudo rpm --import clambhook-release-key.asc
rpm -K ClambHook-1.2.3-x86_64.rpm          # expect "digests signatures OK"

# Ubuntu (debsig-verify package)
sudo install -d /etc/debsig/policies/EAA876B70B1832F5 \
    /usr/share/debsig/keyrings/EAA876B70B1832F5
sudo install -m 0644 clambhook.pol /etc/debsig/policies/EAA876B70B1832F5/
gpg --dearmor < clambhook-release-key.asc | \
    sudo tee /usr/share/debsig/keyrings/EAA876B70B1832F5/debsig.gpg >/dev/null
debsig-verify ClambHook-1.2.3-x86_64.deb
```

`clambhook.pol` is `packaging/debsig/policies/EAA876B70B1832F5/clambhook.pol`.

## Signed package repositories

Installed packages configure the signed repository so `apt` and `dnf` verify
future updates automatically:

- Ubuntu: `/etc/apt/sources.list.d/clambhook.sources` with
  `Signed-By: /usr/share/keyrings/clambhook-archive-keyring.asc`.
- Fedora: `/etc/yum.repos.d/clambhook.repo` with `gpgcheck=1`,
  `repo_gpgcheck=1`, and `/etc/pki/rpm-gpg/RPM-GPG-KEY-clambhook`.

Each release publishes `clambhook-linux-repo-<version>.tar.gz` (signed). Its
`clambhook-linux-repo/` tree must be deployed unchanged to
`https://jpfchang.org/clambhook/linux/` (so `apt/` and `rpm/` sit directly
below that URL). Verify the bundle's `.sig` before deploying.

A successful verification proves integrity and key ownership; it does not
replace platform code-signing, notarization, or package installation checks.
