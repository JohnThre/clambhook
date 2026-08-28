# curl provenance

Clambhook's Android native build downloads and statically links the official
curl 8.18.0 source archive. The archive is not committed to this repository;
`scripts/build-android-curl.sh` stores verified, per-ABI build outputs in the
ignored `ui/android/.native-deps/` cache.

- Version: 8.18.0
- Release archive:
  `https://github.com/curl/curl/releases/download/curl-8_18_0/curl-8.18.0.tar.gz`
- SHA-256:
  `e9274a5f8ab5271c0e0e6762d2fce194d5f98acc568e4ce816845b2dcc0cf88f`
- License: curl license (`LICENSE.txt` in this directory)
- Android floor: API 31 (Android 12)
- Android ABIs: `arm64-v8a`, `armeabi-v7a`, `x86`, `x86_64`

The build enables only HTTP and HTTPS with the pinned static OpenSSL runtime.
It disables the curl executable, shared libraries, tests, examples, manuals,
compression libraries, public-suffix and international-domain libraries,
HTTP/2/3, SSH, and GSSAPI. The native DNS client requests HTTP/2-over-TLS when
available and interoperably falls back to HTTP/1.1 in this minimal build.

To update curl, change the version, release URL, digest, and build-recipe stamp
together in `scripts/build-android-curl.sh`; update this file and
`THIRD_PARTY_NOTICES.md`; delete the ignored matching cache directory; then
rebuild all four ABIs and rerun the API 31/33/36 managed-device matrix.
