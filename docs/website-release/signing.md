# Release signing

ClambHook GitHub Releases use three independent trust layers:

- GPG signs checksums and update manifests with the pinned public key in
  `keys/clambhook-release-key.asc`.
- Apple Developer ID, notarization, stapling, and Sparkle EdDSA protect macOS.
- The stable Android keystore signs APKs; `apksigner verify` runs before upload.

Private material is provided only through the protected `production`
environment, decoded into mode-0600 temporary files, and removed after use.
Never commit signing keys or put their values on compiler command lines.

Users can import the public key from a GitHub Release and verify a checksum:

```sh
gpg --import clambhook-release-key.asc
gpg --verify ClambHook.apk.sha256.sig ClambHook.apk.sha256
sha256sum -c ClambHook.apk.sha256
```
