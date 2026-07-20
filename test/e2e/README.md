# clambhook E2E Tests

These tests are opt-in because they start real proxy/VPN servers and may need
local binaries, Docker, or privileged networking.

Run the convenient local real-server suite (missing optional local tools skip):

```sh
make e2e
```

The scheduled/manual `.github/workflows/e2e.yml` workflow uses the enforcing
command below. It provisions the pinned first-party ClambBack release, requires
usable sing-box, Tor, and ClambBack backends, and fails rather than turning a
missing backend into a green skip:

```sh
make e2e-required
```

OpenVPN real-server coverage may skip in that scheduled suite while the external
backend is absent, but it remains mandatory for the release gate:

```sh
make release-check
```

Run the real Linux daemon TUN path separately on a machine with `/dev/net/tun`,
`ip netns`, Python 3, and root or passwordless sudo:

```sh
make e2e-tun
```

That target creates a network namespace and veth pair, starts the daemon with a
kernel TUN listener, sends a TCP packet from the namespace through first-party
ClambBack, and validates the echoed packet. It performs cleanup on exit.

Useful environment variables:

- `CLAMBHOOK_E2E=1`: required when running `go test -tags e2e` directly.
- `CLAMBHOOK_E2E_BACKEND=auto|local|docker`: selects local binaries or Docker
  for server backends. `auto` prefers local `sing-box`.
- `CLAMBHOOK_E2E_REQUIRE_BACKENDS=1`: require the bundled scheduled-suite
  backends. `make e2e-required` sets this.
- `CLAMBHOOK_E2E_REQUIRE=1`: fail instead of skipping any release-gate backend.
  `make e2e-release` and `make release-check` set this.
- `CLAMBHOOK_BIN=/path/to/clambhook`: daemon binary used by black-box tests.
  Make targets set this automatically.
- `CLAMBBACK_BIN=/path/to/clambback`: override the pinned first-party ClambBack
  binary provisioned by `make provision-clambback-e2e`.
- `CLAMBHOOK_E2E_TUN=1`: enable the privileged Linux TUN test. Prefer
  `make e2e-tun`, which compiles unprivileged and runs only the harness as root.

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
