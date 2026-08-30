#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

set -euo pipefail

ROOT_DIR="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$ROOT_DIR"

fail() {
    echo "cutover check: $1" >&2
    exit 1
}

tracked_go="$(git ls-files '*.go' | while IFS= read -r source; do
    [[ ! -e "$source" ]] || printf '%s\n' "$source"
done)"
[[ -z "$tracked_go" ]] || {
    printf '%s\n' "$tracked_go" >&2
    fail "tracked Go source files remain"
}

for obsolete in go.mod go.sum cmd internal pkg test vendor ui/linux ui/linux-gtk ui/skip; do
    if git ls-files "$obsolete" "$obsolete/**" | while IFS= read -r source; do
        [[ ! -e "$source" ]] || { printf '%s\n' "$source"; break; }
    done | grep -q .; then
        fail "obsolete tracked path remains: $obsolete"
    fi
done

if git grep -nEi \
    '(setup-go|buildGoModule|gomobile|go (build|run|test|vet|install|mod|env)|go\.mod|go\.sum|pkg/cnet|legacy (go )?(oracle|implementation)|clambhook-(c|tui-c|license-c)\b|allow-incomplete-native)' \
    -- . \
    ':(exclude)docs/c-migration.md' \
    ':(exclude)docs/release-validation.md' \
    ':(exclude).github/workflows/ci.yml' \
    ':(exclude)scripts/check-cutover.sh' \
    ':(exclude)scripts/test-outline-interop.sh' \
    ':(exclude)scripts/package-smoke.sh' \
    ':(exclude)scripts/smoke-installed-linux-package.sh'; then
    fail "active build, runtime, or documentation instructions still reference the retired implementation"
fi

# Go remains forbidden in the product. The only exception is the isolated CI
# build of a pinned, official Outline peer used as an interoperability oracle.
grep -Fq 'actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6.0.0' \
    .github/workflows/ci.yml || fail "Outline peer Go toolchain action is not pinned"
grep -Fq 'OUTLINE_COMMIT="4d09f750827738d21432095a46e455d24e172109"' \
    scripts/test-outline-interop.sh || fail "official Outline peer revision is not pinned"
[[ "$(rg -c 'actions/setup-go@' .github/workflows/ci.yml)" == "1" ]] ||
    fail "unexpected Go toolchain actions are active"
if rg -n '(buildGoModule|gomobile|go (build|run|test|vet|install|mod|env)|go\.mod|go\.sum)' \
    .github/workflows/ci.yml; then
    fail "retired product Go tooling returned to CI"
fi
if rg -n '(setup-go|buildGoModule|gomobile|go (build|run|test|vet|install|mod|env)|go\.mod|go\.sum)' \
    scripts --glob '!check-cutover.sh' --glob '!test-outline-interop.sh'; then
    fail "Go tooling escaped the isolated Outline interoperability harness"
fi

if git grep -nEi \
    '(Jetpack Compose|Compose Multiplatform|GTK([ +][0-9]| UI| application| prototype)|libadwaita)' \
    -- . \
    ':(exclude)docs/c-migration.md' \
    ':(exclude)scripts/check-cutover.sh' \
    ':(exclude)scripts/package-smoke.sh' \
    ':(exclude)scripts/smoke-installed-linux-package.sh'; then
    fail "active instructions still reference a retired user-interface stack"
fi

for binary in build-native/clambhook build-native/clambhook-tui build-native/clambhook-license; do
    [[ -f "$binary" ]] || continue
    if command -v readelf >/dev/null 2>&1 && readelf -S "$binary" 2>/dev/null | grep -q '\.go\.buildinfo'; then
        fail "retired runtime build information found in $binary"
    fi
done

grep -Fq '<name>clambhook-ui</name>' ui/javafx/pom.xml ||
    fail "Gluon project name no longer matches the production Linux executable"
grep -Fq '<javafx.static.version>21.0.1</javafx.static.version>' ui/javafx/pom.xml ||
    fail "unexpected Gluon JavaFX static SDK version"
grep -Fq 'GLUON_JAVAFX_STATIC_VERSION ?= 21.0.1' Makefile ||
    fail "Make and Maven disagree on the Gluon JavaFX static SDK version"
grep -Fq '<enableSWRendering>true</enableSWRendering>' ui/javafx/pom.xml ||
    fail "Gluon desktop native images lack the software-rendering fallback"
grep -Fq '44beff405df3719f597e046cbdcd8f8ec245c4813ad3d0f5418e6ab50992231b' \
    scripts/prepare-gluon-linux-aarch64.sh ||
    fail "Linux AArch64 GTK static SDK is not checksum-pinned"
grep -Fq 'd2ba5f26578e4aa81e358f2e9fdf107c1d528294920db4e4a70841a678e49cf4' \
    scripts/patch-gluon-substrate-aarch64.py ||
    fail "Linux AArch64 Substrate input is not checksum-pinned"
if ! grep -Fq 'ORIGINAL_SELECTOR = b"aarch64"' scripts/patch-gluon-substrate-aarch64.py ||
    ! grep -Fq 'GTK_SELECTOR = b"gtkarch"' scripts/patch-gluon-substrate-aarch64.py; then
    fail "Linux AArch64 GTK backend patch changed unexpectedly"
fi
if grep -Fq "printf '!<arch>" scripts/prepare-gluon-linux-aarch64.sh; then
    fail "obsolete empty DRM archive workaround remains"
fi

"$ROOT_DIR/scripts/check-android-abi-policy.sh"

echo "cutover check: all checks passed"
