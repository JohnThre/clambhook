# clambhook E2E Tests

These tests are opt-in because they start real proxy/VPN servers and may need
local binaries, Docker, or privileged networking.

Run the default real-server suite:

```sh
make e2e
```

The `.github/workflows/e2e.yml` workflow runs this suite on a schedule and via
manual dispatch. It uses the Docker sing-box backend by default and installs Tor
on the runner. OpenVPN real-server coverage may skip in the regular suite while
the external backend is absent, but it is mandatory for the release gate.

Run the release-readiness gate:

```sh
make release-check
```

Useful environment variables:

- `CLAMBHOOK_E2E=1`: required when running `go test -tags e2e` directly.
- `CLAMBHOOK_E2E_BACKEND=auto|local|docker`: selects local binaries or Docker
  for server backends. `auto` prefers local `sing-box`.
- `CLAMBHOOK_E2E_REQUIRE=1`: fail instead of skipping when a backend is missing.
  `make e2e-release` and `make release-check` set this.
- `CLAMBHOOK_BIN=/path/to/clambhook`: daemon binary used by black-box SOCKS
  tests. `make e2e` sets this automatically.

For GitHub Actions OpenVPN coverage, configure repository variables
`CLAMBHOOK_E2E_OPENVPN_REMOTE`, `CLAMBHOOK_E2E_OPENVPN_TCP_TARGET`, and
`CLAMBHOOK_E2E_OPENVPN_UDP_TARGET`, plus secrets `CLAMBHOOK_E2E_OPENVPN_CA`,
`CLAMBHOOK_E2E_OPENVPN_CLIENT_CERT`, and `CLAMBHOOK_E2E_OPENVPN_CLIENT_KEY`
containing the PEM material. Release candidates must pass with
`CLAMBHOOK_E2E_REQUIRE=1` so missing OpenVPN coverage fails instead of skipping.

OpenVPN real-server coverage is environment-driven because a local OpenVPN
server usually needs a TUN device and elevated privileges. Point the test at a
local or containerized server with:

```sh
CLAMBHOOK_E2E=1 \
CLAMBHOOK_E2E_OPENVPN_REMOTE=127.0.0.1:1194 \
CLAMBHOOK_E2E_OPENVPN_CA=/path/ca.pem \
CLAMBHOOK_E2E_OPENVPN_CLIENT_CERT=/path/client.pem \
CLAMBHOOK_E2E_OPENVPN_CLIENT_KEY=/path/client.key \
CLAMBHOOK_E2E_OPENVPN_TCP_TARGET=10.8.0.1:9000 \
CLAMBHOOK_E2E_OPENVPN_UDP_TARGET=10.8.0.1:9001 \
go test -tags e2e -run OpenVPN ./test/e2e
```
