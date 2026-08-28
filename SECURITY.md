<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Security Policy

ClambHook is a VPN and proxy router with opt-in HTTP(S) capture, so security
reports are taken seriously and handled privately.

## Reporting a vulnerability

Report suspected vulnerabilities privately by email to **support@swiphtgroup.com**.

- Do not open a public GitHub issue for a security problem.
- Include affected component (daemon, macOS app, privileged helper, Linux app,
  Android app), version or commit, platform, reproduction steps, and impact.
- If you have a proof of concept, attach or describe it; do not post it publicly.

You will receive an acknowledgement, and coordinated disclosure will be arranged
before any public description of the issue. Please allow a reasonable remediation
window before disclosing details elsewhere.

## Scope

The primary supported surfaces are the macOS and GNU/Linux public releases and
the Go/C daemon they embed. Reports against the Android build (internal developer
QA) and the shared source are also welcome.

This reporting policy supplements the rights granted by [`LICENSE`](LICENSE)
and [`LICENSING.md`](LICENSING.md); it does not reduce those rights or authorize
testing against systems or traffic you do not own or have permission to test.
