# lwIP provenance

- Upstream: https://github.com/lwip-tcpip/lwip (official Savannah mirror)
- Release: `STABLE-2_2_1_RELEASE`
- Commit: `009c2256469004009488b3385ba269461e8eb616`
- Archive SHA-256: `ce0b7461c0ad9602c376f0bf07c5eb7253b48c7bf66f011c6bf3e2a96731c539`
- License: BSD-3-Clause; see `LICENSE`
- Imported files: upstream `src/` and `COPYING` (renamed `LICENSE`)
- Local modifications: none under `src/`

The source is pinned and vendored so the same C packet stack can be built for
GNU/Linux, macOS, and Android without a network fetch or a platform package.
ClambHook-specific configuration and adapters live outside this directory.
