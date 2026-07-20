# Project Review and Backlog

A repository-wide review of ClambHook covering the Go/C daemon, protocols and
API, the Apple client (app, privileged helper, widget), the Android and shared
Skip surfaces, the Linux UI and packaging, release automation, CI, tests, docs,
licensing, and repository hygiene. Vendored dependency source is out of scope.

Priorities: **P0** data loss or active compromise; **P1** correctness, security,
or release-contract blocker; **P2** material security, correctness, or coverage
gap; **P3** polish and consistency.

No P0 findings. Six P1 items should block release.

## Now — release and security blockers

- [x] **Secure the loopback control API against local browsers.**
  `internal/api/events_ws.go:42-47` sets `InsecureSkipVerify: true`, disabling
  the WebSocket same-origin check. Empty auth is permitted on loopback binds
  (`internal/api/auth.go:33-56`, `cmd/clambhook/main.go:42-43`), and
  state-changing endpoints (`connect`, `disconnect`, developer CA regenerate)
  take no body. Any local webpage can read the live traffic-metadata stream and
  drive the daemon; the missing `Host` check extends this via DNS rebinding.
  Restore an origin allowlist, validate `Host`, and require an unforgeable
  credential on state-changing routes.
  Done when an arbitrary webpage cannot read `/api/v1/events` or toggle routing.

- [x] **Remove tracked Cloudflare account data.** `.wrangler/cache/wrangler-account.json`
  and `.wrangler/cache/pages.json` are git-tracked and expose a Cloudflare
  account ID and a personal iCloud address in a source-available repo; they are
  also a wrong-repo artifact. Delete `.wrangler/`, add it to `.gitignore`, and
  purge it from reachable history.
  Done when the tree and history contain no Wrangler account cache.

- [x] **Fix the Apple widget license bypass.** `ClambhookWidgetBundle.swift:129-131`
  hardcodes `canUseApp = true`; `ConnectIntent`/`NextProfileIntent` act with no
  license check while `WidgetEnvironment.licenseDecision()` goes unused. An
  expired trial can start the tunnel and switch profiles from the widget. Gate
  every widget action on the license decision; add a daemon-side guard as
  defense in depth.
  Done when an expired trial cannot start routing from any client entry point.

- [x] **Stop proxy-only saves from erasing Enhanced Mode.**
  `ConfigListenSettingsPayload` (`ui/apple/Sources/ClambhookShared/ConfigSettings.swift:64-101`)
  always encodes a `tun` object; `saveProxyPorts` and the system-proxy toggle
  (`SharedApp/SettingsViews.swift:293-301,749-756`) omit current TUN values, so
  the backend (`internal/api/config_settings.go:135-149`) overwrites TUN with
  defaults. Editing proxy ports disables and resets the user's TUN setup. Make
  `tun` optional in partial updates and add a preservation round-trip test.
  Done when changing proxy ports leaves every TUN field unchanged.

- [x] **Repair the Linux release script.** `scripts/release-linux.sh:136`
  expands `$CHAN` under `set -euo pipefail`, but `CHAN` is not assigned until
  line 151, so `make release-linux` aborts before writing or signing the
  manifest. Move the channel normalization above the manifest block.
  Done when the script emits correctly named channel fields and signed checksums.

- [x] **Resolve the Android rule-management stubs.**
  `LocalTunnelApi.kt:100-116` throws `UnsupportedOperationException` for rule
  create/cleanup/ruleset ops in the default embedded mode; the UI exposes these
  actions and `DashboardRepository.performAction` reports the throw as a fake
  "offline" state. Implement runtime-backed mutations (mirroring the existing
  `replaceRules` reload path) or hide unsupported actions, and classify
  unsupported-feature errors separately from connectivity loss.
  Done when every visible rule action works end-to-end in default mode.

## Next — security and correctness

### Backend

- [x] Validate key/nonce/tag lengths before cgo libsodium calls in
  `pkg/cnet/cnet.go`; the `purego` and AES-128 paths already do, so the cgo
  build is silently memory-unsafe on misuse.
- [x] Add malformed-input and cgo-vs-`purego` parity tests for the crypto
  boundary (`pkg/cnet/cnet_test.go`).
- [x] Remove or implement the unused `cnet_buf` "zero-copy pool"
  (`clib/include/cnet.h`, `clib/src/netio.c`); it is dead `malloc`/`free`.
- [x] Make the `test` target depend on `build-clib`; `go test ./...` fails on a
  clean checkout because `internal/api` links against a missing `libcnet.a`.

### Apple

- [x] Stop passing the API bearer token in daemon argv (`DaemonSupervisor.swift:121-130`,
  `ClambhookMacHelper/main.swift:65-80`); argv is world-readable via `ps`. Use
  environment, a file descriptor, or an IPC handshake, and restrict the helper
  log file mode.
- [x] Give the widget the shared keychain access group and entitlement so
  token-enabled widget actions stop failing silently with 401.
- [x] Validate privileged-helper XPC peers via the audit token, not the reusable
  PID (`ClambhookMacHelper/main.swift:141-158`).
- [x] Make system-proxy enablement idempotent and reconcile/restore stale proxy
  state on launch, quit, crash, and license lockout (`MacSystemProxyManager.swift`).
- [x] Remove `MainActor.assumeIsolated` from Sparkle callbacks unless the
  main-thread guarantee is established (`MacSparkleUpdater.swift:47-61`).
- [x] Add app-target tests for helper validation, daemon arguments, proxy
  snapshot restoration, widget intents, license networking, and Sparkle gating.

### Android

- [x] Serialize `ClambhookVpnService` start/stop transitions; unsynchronized
  cross-thread field mutation can leak a freshly established TUN descriptor.
- [x] Handle excluded routes on Android 11/12 (`SDK_INT < TIRAMISU`) or surface a
  blocking warning; today they are dropped and become a full tunnel silently.
- [x] Decide the non-embedded daemon mode: either wire `LocalDaemonService` and
  permit secure loopback transport, or remove the dead service, its
  `FOREGROUND_SERVICE_DATA_SYNC` permission, and the HTTP client mode.
- [x] Test update checksum rejection, update-license gating, license offline
  grace, VPN route/prefix parsing, and `LocalTunnelApi` dispatch.

### Linux and release engineering

- [x] Ship `/etc/clambhook/config.toml` (deb conffile / rpm `%config(noreplace)`)
  or fall back to defaults on missing config; the packaged systemd unit
  crash-loops on a fresh install.
- [x] Add systemd scriptlets/macros and explicit systemd/polkit runtime
  dependencies to RPM and Debian packaging.
- [x] Pin AppImage tooling to immutable versions and verify SHA-256 before
  execution (`packaging/appimage/build-appimage.sh`).
- [x] Commit `flake.lock`; support Linux systems in the flake or document it as
  Darwin-daemon-only.
- [x] Drop `--share=network` from the vendored Flatpak build.
- [x] Replace the `touch`/`sleep` Meson rebuild workaround in `make build-linux`
  with deterministic dependency handling.

## Then — CI and contract integrity

- [ ] Add CI for `make test`, `make lint`, `make test-linux` (Meson), and a
  scheduled/manual protocol `e2e` lane (`test/e2e/README.md` references a
  non-existent `.github/workflows/e2e.yml`).
- [ ] Exercise the real `.deb`/RPM/Flatpak/AppImage recipes in CI, not just
  binary builds.
- [ ] Apply the source-only GitHub publication guard to the Linux release path,
  not only macOS.
- [ ] Reconcile the Android floor: build is `minSdk 30` (Android 11); README
  says "Android 12+".
- [ ] Remove or replace the README link to `AGENTS.md`, which is gitignored and
  absent.
- [ ] Reconcile the README build/test instructions with the LICENSE prohibition
  on building, running, and testing.
- [ ] Add `SECURITY.md` with a coordinated vulnerability-disclosure route.
- [ ] Document whether `release-check` intentionally defers Apple/Android/Linux
  UI test suites to CI.

## Decide / incubate

- [ ] `ui/skip`: integrate into a real client with behavioral tests, or mark it
  explicitly experimental; it is currently unwired and its tests are
  placeholders.
- [ ] Android support-purchase flow: implement the direct-payment path or remove
  the no-op `SupportPurchaseManager` and its wiring.
- [ ] Confirm the Android updater manifest host (apex `clambercloud.com` vs
  documented `store.clambercloud.com`); constrain the download origin or verify
  the APK signing certificate.
- [ ] Sequence v1.1 (network conditioner, protocol-specific viewers) and the
  planned VLESS/VMess/Reality/scripting work only after the blockers above close.

## Verified baseline

- The Go suite passed for every package once `clib/libcnet.a` was built; only the
  clean-checkout sequencing defect above remains.
- Apple/Android full test suites were not run here (environment cost); their
  coverage was assessed by inspection.
- Licensing is implemented in the Go core and the Apple and Android clients; only
  the hosted license server remains external per `docs/license-validation.md`.
- Notable strengths: bounds-checked network parsers, DNS-leak hygiene, atomic
  0600 persistence, disciplined event-bus locking, mobile-runtime resource
  unwinding, encrypted Android secret storage, thorough license/update policy
  tests, hardened Apple release configuration, and enforced source-only GitHub
  releases.
