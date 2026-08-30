<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Contributing

Bug reports and security reports are welcome. Security issues must follow
[`SECURITY.md`](SECURITY.md) and must not be reported publicly.

ClambHook has one C17 runtime, a shared JavaFX/Gluon application for Android
and GNU/Linux, a Kotlin Android platform AAR, and a SwiftUI macOS client. The
CLI, TOML, JSON, HTTP/WebSocket, persistence, identifier, licensing, and release
contracts are compatibility surfaces; describe and test intentional changes to
them explicitly.

## Code and content contributions

Before any third-party code, documentation, design, translation, build change,
or other copyrightable contribution can be merged, the contributor must sign
the current [`CLA.md`](CLA.md). A Developer Certificate of Origin sign-off is
not a substitute.

Signed records are retained privately by Pengfan Chang. After verifying the
record, a maintainer applies the `cla-signed` label to the pull request. Pull
requests from `JohnThre` and trusted dependency automation are exempt; no other
pull request may merge without that label.

If an employer or another entity may own the contribution, the contributor
must provide written authority or arrange a corporate agreement before work is
submitted. Do not place confidential information, third-party code, or material
you lack authority to license in an issue or pull request.

To arrange signature or ask a licensing question, contact
<support@swiphtgroup.com> before submitting the contribution.

## Development expectations

- Preserve SPDX headers and the GPL-3.0-only/Apache-2.0 component boundaries in
  [`LICENSING.md`](LICENSING.md). Do not edit pinned upstream sources unless the
  change is an intentional dependency update with refreshed provenance.
- Add focused regression coverage beside the affected C, Java, Kotlin, or Swift
  implementation. Keep the C17 build warning-clean.
- Use `make test-native`, `make test-javafx`, `make test-android`,
  `make test-apple`, and `make lint` as applicable. Documentation and workflow
  changes must at least pass the cutover, license, GitHub Actions, and staged
  whitespace checks documented in
  [`docs/release-validation.md`](docs/release-validation.md).
- Do not publish local build outputs as official binaries. Only the protected
  GitHub Release workflow may create official installers.

Pull requests should summarize behavior, compatibility and packaging effects,
validation commands, and linked issues. Include screenshots for visible UI
changes and keep secrets, credentials, full license keys, and unredacted proxy
profiles out of issues, patches, logs, and artifacts.
