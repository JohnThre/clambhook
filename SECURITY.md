# Security Policy

ClambHook is a VPN and proxy router with opt-in HTTP(S) capture, so security
reports are taken seriously and handled privately.

## Reporting a vulnerability

Report suspected vulnerabilities privately by email to **clambhook@jpfchang.org**.

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

Given the proprietary, view-only [`LICENSE`](LICENSE), this policy covers
responsible reporting only; it does not grant permission to build, run, deploy,
or redistribute the software.
