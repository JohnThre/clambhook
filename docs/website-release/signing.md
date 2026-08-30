<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Release signing

ClambHook GitHub Releases use three independent trust layers:

- GPG signs checksums and update manifests with the pinned public key in
  `keys/clambhook-release-key.asc` (fingerprint
  `BAFC 7769 FDA1 E0D4 EBD2 3E2F 6FF4 807E AD97 7A9B`).
- Apple Developer ID, notarization, stapling, and Sparkle EdDSA protect macOS.
- The stable Android keystore signs Gluon APK and AAB files; `apksigner verify`
  and `jarsigner -verify -strict` run before upload.

Private material is provided only through the protected `production`
environment, decoded into mode-0600 temporary files, and removed after use.
Never commit signing keys or put their values on compiler command lines.

Users can import the public key from a GitHub Release and verify a checksum:

```sh
gpg --import clambhook-release-key.asc
gpg --verify ClambHook-arm64.apk.sha256.sig ClambHook-arm64.apk.sha256
sha256sum -c ClambHook-arm64.apk.sha256
```

Always download the artifact, checksum, signature, and public key from the same
immutable versioned release. A successful verification proves integrity and key
ownership; it does not replace platform code-signing, notarization, or package
installation checks.
