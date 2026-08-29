<!-- SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com> -->
<!-- SPDX-License-Identifier: GPL-3.0-only -->

# Third-party notices

Clambhook's native builds bundle or build the following upstream components.
Their source provenance is recorded beside each import.

## lwIP 2.2.1

Copyright (c) 2001, 2002 Swedish Institute of Computer Science.
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice,
   this list of conditions and the following disclaimer.
2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.
3. The name of the author may not be used to endorse or promote products
   derived from this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE AUTHOR ``AS IS'' AND ANY EXPRESS OR IMPLIED
WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO
EVENT SHALL THE AUTHOR BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR
BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER
IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
POSSIBILITY OF SUCH DAMAGE.

Upstream and archive details: `third_party/lwip/README.clambhook.md`.

## tomlc99

MIT License

Copyright (c) CK Tan

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
of the Software, and to permit persons to whom the Software is furnished to do
so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

Upstream and commit details: `third_party/tomlc99/README.clambhook.md`.

## llhttp 9.4.3

Copyright (c) 2018 Fedor Indutny.

Licensed under the MIT License. ClambHook compiles the pinned generated parser
as a private static library. The complete license, release archive digest, and
upstream details are in `third_party/llhttp/`.

## curl 8.18.0

Copyright (c) 1996 - 2025, Daniel Stenberg and contributors. All rights
reserved.

Licensed under the curl license. The complete license is distributed at
`third_party/curl/LICENSE.txt` in source checkouts and at
`licenses/curl/LICENSE.txt` in Android package assets.

The Android build downloads the official release archive, validates its
SHA-256 digest, and statically links an HTTP(S)-only per-ABI library. Version,
archive, digest, configuration, and update details are recorded in
`third_party/curl/README.clambhook.md`.

## OpenSSL 3.5.8 LTS

Copyright 1998-2026 The OpenSSL Project Authors. All Rights Reserved.

Licensed under the Apache License 2.0. The complete license is distributed at
`third_party/openssl/LICENSE.txt` in source checkouts and at
`licenses/openssl/LICENSE.txt` in installed/package assets.

The Android build downloads the official release archive, validates its
SHA-256 digest, and statically links the resulting per-ABI libraries. Version,
archive, digest, configuration, and update details are recorded in
`third_party/openssl/README.clambhook.md`.

## libmaxminddb 1.13.3

Copyright MaxMind, Inc.

Licensed under Apache-2.0. ClambHook compiles the pinned reader as a private
static dependency. The complete license, upstream version, archive digest, and
vendored-file inventory are in `third_party/libmaxminddb/`.

## wireguard-lwip

Copyright the wireguard-lwip contributors and the credited reference-crypto
authors.

Licensed under BSD-3-Clause, with the separate X25519 notice retained beside
that source. The exact revision and imported-file boundary are in
`third_party/wireguard_lwip/PROVENANCE.md`.

## OpenJFX 21.0.12

Copyright Oracle and/or its affiliates and OpenJFX contributors.

JavaFX base, graphics, and controls are licensed under GPL-2.0-only with the
Classpath exception where designated by their source headers. ClambHook's
Maven API/runtime artifacts are pinned at 21.0.12. Gluon's independently
published static JavaFX 21 substrate is pinned at 21.0.1 for native targets.
Source and license information: https://github.com/openjdk/jfx

## GluonFX 1.0.29 and Substrate 0.0.69

Copyright Gluon.

The GluonFX Maven plugin is BSD-3-Clause and is used only at build time.
Substrate source used to create the native launcher declares GPL-3.0-or-later
in its source headers. Exact Maven versions are pinned in `ui/javafx/pom.xml`.
Source and license information:

- https://github.com/gluonhq/gluonfx-maven-plugin
- https://github.com/gluonhq/substrate

## Android/Kotlin dependencies

The Android application and platform AAR use AndroidX Activity 1.11.0, Core
1.19.0, DataStore 1.1.1, Security Crypto 1.1.0, Kotlin 2.4.10,
kotlinx.coroutines 1.11.0, kotlinx.serialization 1.11.0, OkHttp 5.5.0, and ZXing
Android Embedded 4.3.0. These components are licensed under Apache-2.0 and
retain their upstream notices. JUnit and AndroidX Test dependencies are
test-only and are not shipped in product packages. Exact coordinates and
scopes are recorded in
`packaging/sbom.cdx.json` and the Gradle build.

## GraalVM Community Edition 17.0.9

GraalVM is a checksum-pinned build tool and is not shipped as a JRE or SDK in
ClambHook packages. Upstream license and component notices accompany the
official GraalVM archive. Archive URLs and per-platform SHA-256 values are
recorded in `scripts/provision-graalvm17.sh`.
