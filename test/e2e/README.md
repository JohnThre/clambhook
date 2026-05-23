# clambhook E2E Tests

These tests are opt-in because they start real proxy/VPN servers and may need
local binaries, Docker, or privileged networking.

Run the default real-server suite:

```sh
make e2e
```

Useful environment variables:

- `CLAMBHOOK_E2E=1`: required when running `go test -tags e2e` directly.
- `CLAMBHOOK_E2E_BACKEND=auto|local|docker`: selects local binaries or Docker
  for server backends. `auto` prefers local `sing-box`.
- `CLAMBHOOK_E2E_REQUIRE=1`: fail instead of skipping when a backend is missing.
- `CLAMBHOOK_BIN=/path/to/clambhook`: daemon binary used by black-box SOCKS
  tests. `make e2e` sets this automatically.

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
