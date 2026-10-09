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

The complete license text is in [`LICENSE-APACHE`](LICENSE-APACHE), with local
copies beside each library. These libraries may be used in open-source or
proprietary applications subject to Apache-2.0's conditions. Their public APIs
carry no additional compatibility or support guarantee beyond the source and
release documentation.

## GPL-3.0-only application core

All other first-party material in this repository is licensed under the GNU
General Public License version 3 only (`GPL-3.0-only`). This includes the C17
runtime and public ABI, the `clambhook` daemon and `clambhook-license` helper,
build and release tooling, documentation, configuration, packaging, and
first-party assets. The complete license text is
in [`LICENSE`](LICENSE).

SPDX declarations in a file, and the exceptions recorded in `REUSE.toml`, take
precedence over this directory-level summary.

## ClambHook apps

The ClambHook apps — the SwiftUI macOS app, the Kotlin/Compose Android app and
GNU/Linux desktop controller (`clambhook-ui`), the `clambhook-tui` terminal
client, and the Android JNI bridge — are not part of this repository. From
version 1.1.0 they are proprietary and are developed in a private repository.
Versions 1.0.2 and earlier of those clients were published here under
GPL-3.0-only; copies obtained under that license keep it, and that history
remains in this repository.

The apps bundle, link, or launch this core. The copyright holder distributes
them under the separate terms described below. Every app build ships this
core's license texts and points to this repository for its complete
corresponding source. Nothing in the apps' terms limits the GPL rights that
attach to this core.

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

Files under `third_party/**` and other expressly identified upstream material remain under
their respective licenses. See [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md)
and the notices distributed beside those files. No first-party license changes
an upstream copyright or license.
