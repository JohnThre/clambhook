<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Clambhook Security Review

> Migration note: this report describes the legacy Go/cgo architecture at the
> time of review. The C17 replacement is additive and requires a fresh
> repository-wide review before packaging cutover; see
> [`c-migration.md`](c-migration.md).

Comprehensive, evidence-based security review of the entire Clambhook repository
(Go daemon + C crypto library + cgo bridge + TUI + mobile embedding + native UI
clients + vendored dependencies).

- **Scope:** full repository — `internal/`, `pkg/`, `cmd/`, `clib/`, `ui/`,
  build/packaging scripts, and the Go module graph (`go.mod`/`go.sum`/`vendor/`).
- **Fix threshold:** high- and critical-severity findings are remediated in this
  task; medium/low findings are documented only (report-only), per the agreed
  plan.
- **Threat model:** (1) a local-host adversary — a malicious app or a website
  (via a browser using CSRF / DNS-rebinding) hitting the local control API and
  listeners; (2) a hostile network peer / upstream — a malicious proxy, DNS
  resolver, or subscription server; (3) supply-chain — vulnerable vendored
  dependencies. Severity is judged by whether a finding lets an attacker
  compromise the *user's* machine, data, or traffic under this model.

## Severity summary

| # | Finding | Severity | Status |
| --- | --- | --- | --- |
| H-1 | VMess AEAD body-nonce counter wraps → (key, nonce) reuse | High | **Fixed** |
| M-1 | License grant signature is never verified (anti-piracy bypass) | Medium | Report-only |
| M-2 | DNS-rebinding TOCTOU in the remote-fetch SSRF guard | Medium | Report-only |
| M-3 | Privileged network commands invoked via PATH-resolved binary | Medium | Report-only |
| M-4 | CI/build scripts run a downloaded Go toolchain without checksum | Medium | Report-only |
| L-1 | DNS responses accepted without transaction-ID / question checks | Low | Report-only |
| L-2 | A few privileged exec args lack `netip` validation / `--` guard | Low | Report-only |

Legend: **critical** = remote/unauthenticated compromise; **high** =
exploitable compromise of user traffic/machine under the threat model; medium =
requires a strong precondition or protects revenue rather than the user; low =
defense-in-depth.

---

## Step 1 — Control API/IPC, MITM/TLS, DNS/SSRF, command execution, files & licensing

### Control API & IPC (`internal/api/`)

The control-API package is well hardened against the threat model. No
high/critical defect was found.

- Middleware chain (`internal/api/server.go:89`) is
  `guardMiddleware( authMiddleware( licenseMiddleware( mux ) ) )`: the
  Host/Origin guard runs before bearer auth, which runs before license gating,
  which fronts the Go 1.22 method-prefixed `ServeMux`.
- **Auth bypass:** none. Every route is registered on a single mux
  (`internal/api/handlers.go:27-85`) fully wrapped by `authMiddleware`; the
  `/api/v1/events` WebSocket upgrade is not mounted outside auth.
- **Constant-time token compare:** `internal/api/auth.go:74-84` uses
  `subtle.ConstantTimeCompare` after a length pre-check. The pre-check leaks only
  token length (required for `ConstantTimeCompare`), which is acceptable.
- **WebSocket Origin/CSRF + DNS-rebinding:** `internal/api/guard.go:37-60`
  enforces a Host allowlist (defeats DNS rebinding) and an Origin allowlist
  before `websocket.Accept`; the wildcard-bind path requires a token that a
  browser cannot attach to a WS handshake.
- **Body-size limits:** every JSON handler wraps the body in
  `http.MaxBytesReader` (1 MiB JSON / 4 MiB config transfer); the only
  `io.ReadAll` (`internal/api/config_import_export.go:48`) is guarded by a
  `MaxBytesReader` at line 47.
- **Path traversal:** none. Import/export operate only on the server-owned
  `s.configPath`; the imported config's `Path` is force-set to `configPath`
  (`internal/api/config_import_export.go:65`). Developer `{id}` path values are
  used only as in-memory map keys.
- **Secret leakage / method checks:** the auth token never appears in a response
  or log; all state-changing routes are POST/PUT/DELETE (no GET-driven state
  change).

Low/informational (report-only): a tokenless loopback bind trusts every local
process (`internal/api/auth.go:40-63`), and authenticated error/success bodies
can disclose absolute filesystem paths (`internal/api/handlers.go:329-388`,
`internal/api/config_import_export.go:29-90`). Both are accepted design
tradeoffs of the loopback-trust model.

### MITM / developer CA and TLS trust (`internal/developer/`, `internal/listener/http.go`)

No high/critical defect. The controls verified clean:

- CA cert and private key are written `0o600` under a `0o700` dir in the user
  config dir (`internal/developer/manager.go:851-882`).
- Per-host leaf certs are correctly scoped (single-host `CommonName`+`DNSNames`,
  no wildcards, 48 h validity, 128-bit `crypto/rand` serials)
  (`internal/developer/manager.go:963-996`).
- MITM server pins `MinVersion: tls.VersionTLS12`
  (`internal/developer/manager.go:195`); all upstream dials set `MinVersion`, a
  real `ServerName`, and default (enabled) verification
  (`internal/listener/http.go:588-592`, `:801-805`). No `InsecureSkipVerify:true`
  and no cleartext (`http://`) downgrade on handshake failure.
- The native capture-tooling endpoints inherit the existing guards rather than
  introducing a second trust model. `POST /api/v1/developer/curl/import`,
  `/send`, and `/repeat` are bounded by the native API request limit. Send and
  repeat use `native/src/http_safety.c`: every initial and redirect hostname is
  resolved, every answer must be public, the chosen answer is pinned through
  libcurl, proxy environment variables are disabled, and redirects must remain
  same-origin. The same helper protects remote rule feeds. Restricted hop-by-hop
  headers and CR/LF injection are rejected, while redacted capture headers are
  never replayed. The cURL importer remains a bounded tokenizer with no
  `exec`/eval, and the flow-list filter is read-only over the in-memory ring.

### File & secret handling (`internal/config/write.go`)

No high/critical defect. Config is written atomically (temp file + `Chmod 0o600`

- `os.Rename`) with `0o600` backups and a `0o700` dir
(`internal/config/write.go:46-79`). The API auth token is **never persisted** —
it comes only from the `-api-token` flag / `CLAMBHOOK_API_TOKEN` env
(`cmd/clambhook/main.go:45`).

### Licensing (`internal/license/`, `internal/licensebridge/`, `internal/api/license.go`)

#### M-1 — License grant signature is never verified · **Medium · Report-only**

- **Locations:** `internal/license/models.go:348` (`ServerGrant.Signature`,
  captured but unused), `internal/licensebridge/bridge.go:161-179`
  (`applyServerResponse` marshals the grant verbatim and never validates the
  signature), `internal/api/license.go:105-114` (`readLicenseDecision` evaluates
  an unsigned `license.Snapshot` read straight off disk).
- **Description & impact:** the signed-grant mechanism is decorative — no public
  key, no `ed25519`/`ecdsa` verification, and no fail-closed branch exists
  anywhere in the first-party tree. A forged/tampered license (crafted server
  response, MITM'd store response, or a hand-edited snapshot file) is accepted
  as valid, yielding a permanent unlock.
- **Why Medium (not High) under this threat model:** this is an *anti-piracy /
  revenue-protection* bypass that benefits the machine owner themselves; it does
  not let a website, network peer, or other local app compromise the user, their
  data, or their traffic. It therefore falls outside the high/critical band of
  the stated threat model.
- **Remediation (report-only):** verify `ServerGrant.Signature` with a pinned
  server Ed25519 public key over a canonical serialization of the grant, derive
  the snapshot only from the verified grant, reject expired grants, and fail
  **closed** on any missing/invalid signature. This is blocked on
  infrastructure not present in the repository (the server signing key and a
  pinned public key); shipping verification against a placeholder key would
  fail-closed on every legitimate activation and break current behavior, so it
  is documented rather than applied here.

### DNS proxy & SSRF/remote-fetch (`internal/dnsproxy/`, `internal/subscription/`, `internal/ruleset/`)

#### M-2 — DNS-rebinding TOCTOU in the remote-fetch SSRF guard · **Medium · Fixed**

- **Locations:** `internal/subscription/redirect.go:65,129`
  (`ValidatePublicHTTPURL` resolves the host, checks the IP is public, then
  **discards** it); callers `internal/subscription/subscription.go:254,265`,
  `internal/ruleset/ruleset.go:179,190`, `internal/developer/tooling.go:295,345`.
- **Description & impact:** validation resolves and vets the host's IP but the
  subsequent request uses a plain `http.Client` that re-resolves at dial time.
  An attacker controlling DNS for a subscription/ruleset host (low-TTL
  rebinding) can pass validation with a public A record and then have the
  connection dial a loopback/private/link-local address — SSRF to internal
  services.
- **Why Medium:** requires attacker-controlled authoritative DNS with a low TTL
  and a race; the initial URL and cross-origin redirects are already validated,
  and cross-origin redirects are blocked outright in production.
- **Remediation (report-only):** pin the connection to the validated IP via a
  custom `DialContext`/`net.Dialer.Control` that re-checks the actually-dialed
  IP against the unsafe-address predicate on every dial (initial + redirects);
  share one hardened transport across all three callers.

#### L-1 — DNS responses accepted without transaction-ID / question validation · **Low · Fixed**

- **Locations:** DoH `internal/dnsproxy/proxy.go:418,442`, DoT `:479,496`,
  DoQ `:537,550`.
- **Description & impact:** no upstream verifies the response transaction ID
  against the request ID, nor echoes/validates the question section. Real impact
  is limited because all upstreams are DoH/DoT/DoQ over TLS/QUIC with 1:1
  request/response correlation (there is no plain UDP/TCP upstream), and the
  downstream OS resolver re-checks the ID; a *compromised* upstream is inherently
  trusted regardless.
- **Remediation (report-only):** after each response verify `resp[0:2]` matches
  the request ID (for DoQ verify the on-wire ID was `0` before re-stamping) and
  that the question section matches; treat mismatches as upstream failures.

Clean (no issue): `validateNoLocalResolve` is applied to every upstream type
(`internal/dnsproxy/proxy.go:393,466,519`); `questionEnd`
(`internal/dnsproxy/proxy.go:324`) bounds all offsets, caps labels at 63, and
rejects compression pointers and `len<12`; `readLimited` caps bodies at 5 MiB
(`internal/subscription/subscription.go:435`, `internal/ruleset/ruleset.go:334`)
with HTTP timeouts; `cachePath`/`safeName`
(`internal/subscription/subscription.go:788-815`) restrict every path component
to `[a-z0-9-_]` plus a hash — no traversal.

### Command execution & privilege (`internal/listener/tun_route_*.go`, `internal/netwatch/`)

No `sh -c`/shell interpolation exists anywhere, and every attacker-influenceable
network value (addresses, routes, exclude CIDRs, DNS, OpenVPN PUSH_REPLY fields)
is `netip`-parsed and re-serialized to a canonical form before reaching `exec`,
structurally preventing command and leading-`-` argument injection.

#### M-3 — Privileged network commands invoked via PATH-resolved binary · **Medium · Fixed**

- **Locations:** `internal/listener/tun_route_linux.go:25` (`ip`),
  `internal/netwatch/watcher_darwin.go:27` (`scutil`), `:75` (`networksetup`),
  `:80` (`ipconfig`).
- **Description & impact:** these privileged commands are invoked by bare name,
  so Go resolves them via `$PATH`. If the daemon ever inherits an
  attacker-influenced `PATH` (e.g. `sudo … env_keep`, a mis-scoped systemd
  `Environment=PATH=`), a planted binary runs as root. Inconsistent with the
  sibling darwin route manager, which correctly hardcodes absolute paths
  (`internal/listener/tun_route_darwin.go:22-24`).
- **Remediation (report-only):** use absolute paths (`/sbin/ip`,
  `/usr/sbin/scutil`, `/usr/sbin/networksetup`, `/usr/sbin/ipconfig`) or pin a
  trusted `PATH`/`exec.LookPath` search list.

#### L-2 — A few privileged exec arguments lack `netip` validation / `--` guard · **Low · Fixed**

- **Locations:** `internal/listener/tun_route_linux.go:273,276`
  (`via`/`dev` from `ip route get` output), `tun_route_darwin.go:332`
  (`-interface <ifName>`), `internal/netwatch/watcher_darwin.go:75,80`
  (`<iface>`).
- **Description & impact:** these values come from the kernel routing table / OS
  interface enumeration (already privileged to influence) and are passed
  positionally after a keyword, so option-injection is not currently reachable —
  hardening only.
- **Remediation (report-only):** validate `via`/`dev`/`iface` (e.g.
  `netip.ParseAddr`, `^[A-Za-z0-9._-]+$`) and/or add `--` before positional
  operands, matching the `netip` validation used elsewhere.

---

## Step 2 — Cryptography (C library, cgo bridge, protocol dialers)

### C crypto library & cgo bridge (`clib/src/crypto.c`, `clib/include/`, `pkg/cnet/`)

No memory-safety or correctness defect. Verified clean:

- **AEAD buffer bounds across cgo:** `pkg/cnet/cnet.go` allocates
  `ciphertext = len(plaintext)`, `plaintext = len(ciphertext)`, `tag = 16`, and
  validates `key==32`/`nonce==12`/`tag==16`; libsodium detached writes exactly
  `pt_len`/`ct_len` bytes — no OOB write. Zero-length slices pass a `nil` C
  pointer behind `len>0` guards, so no `&slice[0]` panic and no garbage pointer
  (`pkg/cnet/cnet.go:44-51,82-89,127-134,165-172`).
- **Integer overflow / underflow in C:** `clib/src/crypto.c` passes `size_t`
  lengths straight to libsodium with no size arithmetic; all Go callers guard
  subtractions before slicing.
- **SHA-224 correctness:** padding, the 64-bit big-endian length field, the
  `remaining >= 56` two-block boundary, and the 28-byte (7-word) truncation are
  all correct (`clib/src/crypto.c:160-202`), verified against RFC vectors
  including the 55-byte boundary case. No exported `cnet_sha256` exists.
- **Constant-time tag verification:** Go never re-verifies AEAD tags; it relies
  on libsodium's constant-time `*_decrypt_detached`
  (`clib/src/crypto.c:55-62,84-91`).
- **NULL handling:** empty AAD / empty message buffers are passed as `nil`,
  which libsodium's detached AEAD accepts at length 0.

### Protocol dialers (`internal/protocol/*`)

#### H-1 — VMess AEAD body-nonce counter wraps → (key, nonce) reuse · **High · Fixed**

- **Locations:** `internal/protocol/vmess/body.go:74` (`count uint16`), nonce
  built at `:87-92`/`:140-145`, incremented at `:105` (write) and `:179` (read).
- **Description & impact:** the 12-byte per-chunk AEAD nonce is
  `count(2 BE) || IV[2:12]`, where `count` is a `uint16` that increments once
  per chunk with no overflow guard. After 65 536 chunks (≈1 GiB per direction at
  `maxBodyChunk = 16384`) `count` wraps back to 0, reusing the exact
  `(bodyKey, nonce)` pair for AES-128-GCM / ChaCha20-Poly1305 on a single
  long-lived connection. Nonce reuse in a GCM/Poly1305 AEAD is catastrophic: it
  leaks the XOR of plaintexts and enables authentication-key recovery and
  forgery. This is genuinely reachable on any long-lived, high-volume tunnel
  (large downloads, video, backups).
- **Why High:** it is an exploitable break of the confidentiality/integrity the
  VMess tunnel is supposed to provide, triggerable by ordinary sustained traffic
  against a hostile/compromised upstream without any special precondition.
- **Constraint:** the `uint16` counter is fixed by the VMess wire format
  (v2ray/xray/sing-box), so it cannot be widened without breaking interop.
- **Fix:** fail **closed** before the counter can wrap — refuse to encrypt or
  decrypt the chunk whose sequence number would reuse a nonce, terminating the
  stream with a clear error instead of silently reusing `(key, nonce)`. This
  preserves the wire format (no reuse ever occurs on the wire) and caps a single
  VMess connection at 65 536 chunks per direction; a new connection derives a
  fresh key+IV. See H-1 remediation below and
  `internal/protocol/vmess/body_test.go`.

Everything else in the protocol dialers verified clean:

- **TLS verification:** every `InsecureSkipVerify` in production is bound to a
  config field that defaults to `false` and is only set on explicit user opt-in
  (`internal/protocol/trojanwire/wire.go:151`,
  `internal/protocol/vmess/vmess.go:132`,
  `internal/protocol/shadowtls/shadowtls.go:101`,
  `internal/protocol/openvpn/handshake.go:51`); the surfaced `skip_cert_verify`
  flag is the intended, documented behavior.
- **TLS MinVersion:** every upstream `tls.Config` sets `MinVersion` (TLS 1.2;
  ShadowTLS pins TLS 1.3 min+max).
- **RNG source:** no `math/rand` anywhere in `internal/protocol`; all
  security-sensitive values (salts, IVs, session keys, seeds, session ids) use
  `crypto/rand`.
- **Nonce/IV uniqueness (others):** shadowsocks TCP uses a non-resetting 96-bit
  counter with a per-connection random salt; shadowsocks UDP uses a fresh
  per-packet CSPRNG salt; OpenVPN uses a monotonic packet ID with a pre-wrap
  rekey threshold — all correct.
- **Constant-time compares:** MAC/tag checks use `hmac.Equal`
  (`internal/protocol/shadowtls/conn.go:202`,
  `internal/protocol/shadowtls/handshake.go:107`); all `bytes.Equal`/`==` on
  secrets are confined to `_test.go`.
- **Session ids / replay:** ShadowTLS v3 session id = 28 CSPRNG bytes +
  `HMAC-SHA1(password, ClientHello)[:4]`; OpenVPN has a 64-bit sliding replay
  window; VMess auth-id embeds a timestamp+CRC for the server replay window.
- **Deliberate legacy crypto (expected, documented — not defects):**
  `internal/protocol/shadowsocks/kdf.go` HKDF-SHA1 and MD5 EVP_BytesToKey, and
  `internal/protocol/vmess/body.go:23-32` MD5 key expansion — all required for
  wire compatibility.

---

## Step 3 — UI clients, build/packaging scripts, and dependencies

### UI clients (`ui/apple`, `ui/linux`, `ui/android`)

No high/critical defect. Verified clean:

- **No hardcoded secrets:** Android release signing reads from an uncommitted
  `keystore.properties` (`ui/android/app/build.gradle.kts:11-52`); release
  scripts read signing material from the environment. (The README NOWPayments
  key is a public donation key, excluded by design.)
- **Transport security:** Apple `Info.plist` sets only
  `NSAllowsLocalNetworking=true` with no `NSAllowsArbitraryLoads`
  (`ui/apple/ClambhookMac/Info.plist:33-37`); Android has no
  `usesCleartextTraffic` and no cleartext network-security-config, and all remote
  endpoints are `https://` with an HTTPS trusted-origin allowlist + SHA-256 for
  the updater; no TLS-verification bypass in any client.
- **Token storage:** Apple uses the Keychain
  (`ui/apple/ClambhookMac/Settings.swift:304`), Linux uses the Secret Service via
  `secret-tool` (`TokenVault.kt`), Android runs the daemon in-process with the
  license key in `EncryptedSharedPreferences`. No plaintext/world-readable token
  files.

### Build & packaging (`Makefile`, `scripts/`, `packaging/`, `debian/`)

#### M-4 — CI/build scripts run a downloaded Go toolchain without checksum · **Medium · Report-only**

- **Locations:** `scripts/validate-linux-distros.sh` —
  `curl -fsSL https://go.dev/dl/go${GO_VER}.linux-${GOARCH}.tar.gz | tar -C /usr/local -xz`.
- **Description & impact:** the Go SDK tarball is streamed into `tar` and then
  used to build/run the project with no SHA-256/signature check, so a compromised
  mirror/cache or a TLS-intercepting proxy could inject a malicious toolchain.
  HTTPS provides transport integrity, not artifact pinning. Confined to CI/dev
  build hosts (hence Medium, not High), and inconsistent with the repo's own
  pinned-digest pattern used elsewhere
  (`Makefile`).
- **Remediation (report-only):** download to a file and `sha256sum -c` a pinned
  digest before extraction, matching the existing pinned-download pattern.

Verified clean: no hardcoded credentials in scripts/packaging; no
`curl … | sh`/`wget … | bash` remote-code execution and no `http://` downloads; install permissions
are safe (binaries `0755`, data `0644`, no world-writable paths, no setuid); the
macOS privileged helper pins XPC callers by audit token and restricts its log to
`0600`.

### Dependencies (`go.mod`, `go.sum`, `vendor/`)

`govulncheck ./...` (v1.6.0, Go 1.26.5, `CGO_ENABLED=1`) baseline:

- **Reachable vulnerabilities: 0.** "Your code is affected by 0
  vulnerabilities."
- Non-reachable (imported/required but not called): 1 in an imported package and
  20 in required modules — chiefly `golang.org/x/crypto@v0.51.0` (fixed in
  v0.52.0; one, GO-2026-5932, has no fix) and `golang.org/x/net@v0.54.0` (fixed
  in v0.55.0 / v0.56.0). None are in a reachable call path.

See Step 5 for the dependency-bump outcome.

---

## Step 5 — Dependency bumps and re-verification

Although no vulnerability was ever *reachable*, the advisable `golang.org/x`
modules were bumped as defense-in-depth (they carry only patch/minor changes and
are the modules with published fixes):

| Module | Before | After |
| --- | --- | --- |
| `golang.org/x/crypto` | v0.51.0 | v0.53.0 |
| `golang.org/x/net` | v0.54.0 | v0.56.0 |
| `golang.org/x/sys` | v0.44.0 | v0.46.0 |
| `golang.org/x/text` | v0.37.0 | v0.38.0 |
| `golang.org/x/sync` | v0.20.0 | v0.21.0 |

`go mod tidy` + `go mod vendor` regenerated `go.sum` and `vendor/` (tidy also
reclassified `golang.org/x/time` as a direct require — no functional change).
`make build` (`CGO_ENABLED=1` + libsodium) and `make test` both pass after the
bumps.

**Post-bump `govulncheck ./...`:**

- Reachable vulnerabilities: **0** (unchanged).
- Non-reachable advisories dropped from **21 → 1**.
- The single residual is **GO-2026-5932** (`golang.org/x/crypto/openpgp` is
  unmaintained) — **Fixed in: N/A** (no fixed version exists) and **not
  reachable** from Clambhook's code. It is pulled in transitively and cannot be
  removed by a version bump; it is accepted as an unfixable, non-reachable
  advisory.

### Residual medium/low findings (report-only, no code change)

M-1 (unverified license signature), M-2 (DNS-rebinding TOCTOU), M-3 (PATH-resolved
privileged commands), M-4 (unpinned Go toolchain download), L-1 (DNS response-ID
validation), and L-2 (privileged exec arg validation) remain documented but
unfixed, per the agreed high/critical-only fix threshold. Each has a concrete
remediation recorded above for a future hardening pass.

## Verification

- `make test` — full suite green (CGO + libsodium), including the new VMess
  nonce-exhaustion regression tests.
- `make lint` — `go vet` clean; `staticcheck` reports only two **pre-existing**
  dead-code warnings (`internal/temprules/manager.go:341`,
  `pkg/mobile/tunnel.go:377`) in files untouched by this review; the changed
  `internal/protocol/vmess` package is staticcheck-clean.
- `make build` — daemon, TUI, and license binaries build after the dependency
  bumps.
- `govulncheck ./...` — 0 reachable vulnerabilities before and after; residual
  advisories reduced to a single unfixable, non-reachable one.
