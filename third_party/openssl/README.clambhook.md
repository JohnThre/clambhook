# OpenSSL provenance

ClambHook's Android native build downloads and statically links the official
OpenSSL 3.5.8 LTS source archive. The archive is not committed to this
repository; `scripts/build-android-openssl.sh` stores verified, per-ABI build
outputs in the ignored `ui/android/.native-deps/` cache.

- Version: 3.5.8
- Release archive:
  `https://github.com/openssl/openssl/releases/download/openssl-3.5.8/openssl-3.5.8.tar.gz`
- SHA-256:
  `a8f84a39918ec6415ce765d9b429d313ba97b8143169c172e734b9514464f5b2`
- License: Apache License 2.0 (`LICENSE.txt` in this directory)
- Android floor: API 31 (Android 12)
- Build-script ABIs: `arm64-v8a`, `armeabi-v7a`, `x86`, `x86_64`
- Product ABI: `arm64-v8a`; hosted managed-device builds add only a debug
  `x86_64` slice

The build disables shared libraries, command-line applications, tests,
documentation, the legacy provider, weak SSL ciphers, loadable modules, DSO,
and automatic configuration loading. TLS, QUIC, and the default cryptographic
provider remain statically available to the native protocol and DoQ
implementations.

To update OpenSSL, change the version, download URL, and digest together in
`scripts/build-android-openssl.sh`; update this file and
`THIRD_PARTY_NOTICES.md`; delete the ignored matching cache directory; then
rebuild every ABI required by native portability checks and rerun the product
ARM64 plus API 31/33/36 x86_64 managed-device gates. Physical-device testing
is optional supplemental QA.
