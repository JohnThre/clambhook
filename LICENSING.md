<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Licensing

Copyright 2026 Pengfan Chang <support@swiphtgroup.com>.

ClambHook uses a component-based licensing model. This document is the
authoritative map for first-party material in this repository.

## Apache-2.0 reusable libraries

The following first-party directories, including their source, headers, build
files, documentation, and tests, are licensed under the Apache License 2.0:

- `clib/**`
- `pkg/cnet/**`

The complete license text is in [`LICENSE-APACHE`](LICENSE-APACHE), with local
copies beside each library. These libraries may be used in open-source or
proprietary applications subject to Apache-2.0's conditions. Their public APIs
carry no additional compatibility or support guarantee beyond the source and
release documentation.

## GPL-3.0-only application core

All other first-party material in this repository is licensed under the GNU
General Public License version 3 only (`GPL-3.0-only`). This includes the native
runtime and public ABI, Go application packages, mobile bridge, command-line
programs, graphical applications, build and release tooling, documentation,
configuration, packaging, and first-party assets. The complete license text is
in [`LICENSE`](LICENSE).

SPDX declarations in a file, and the exceptions recorded in `REUSE.toml`, take
precedence over this directory-level summary.

## Separate commercial licensing

Pengfan Chang is the copyright holder of the first-party application core and
may offer it under separate written commercial terms, including terms that
permit incorporation into proprietary applications. The public repository does
not itself grant such proprietary rights: absent a separate signed agreement,
the application core is available under GPL-3.0-only.

Commercial licensing enquiries: <support@swiphtgroup.com>.

Official signed ClambHook builds, update access, activation, and support may be
sold under separate end-user terms. This does not limit the GPL rights attached
to source obtained under GPL-3.0-only, including the right to build, modify, and
redistribute compliant versions. It also does not grant a right to use the
ClambHook trademarks or present a modified build as an official build.

## Third-party material

Files under `vendor/**` and `third_party/**`, Gradle wrapper files identified in
`REUSE.toml`, and other expressly identified upstream material remain under
their respective licenses. See [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md)
and the notices distributed beside those files. No first-party license changes
an upstream copyright or license.
