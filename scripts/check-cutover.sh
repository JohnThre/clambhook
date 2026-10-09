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

for obsolete in go.mod go.sum cmd internal pkg test vendor ui/linux ui/linux-gtk ui/skip \
    ui/javafx ui/android; do
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
    ':(exclude).github/dependabot.yml' \
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

# The Android and GNU/Linux GUI is Kotlin only (Compose Multiplatform in
# ui/kotlin). JavaFX, Gluon, GraalVM native-image, GTK, and Java UI sources are
# retired and must not return.
if git ls-files 'ui/*.java' 'ui/**/*.java' 'ui/**/pom.xml' | grep -q .; then
    git ls-files 'ui/*.java' 'ui/**/*.java' 'ui/**/pom.xml' >&2
    fail "Java or Maven UI sources remain; the Android and GNU/Linux GUI is Kotlin only"
fi
if git grep -nEi \
    '(javafx|gluonfx|gluon|graalvm|native-image|GRAALVM_HOME|GTK([ +][0-9]| UI| application| prototype)|libadwaita)' \
    -- Makefile CMakeLists.txt ui scripts .github debian packaging \
    ':(exclude)scripts/check-cutover.sh' \
    ':(exclude)scripts/package-smoke.sh' \
    ':(exclude)scripts/smoke-installed-linux-package.sh' \
    ':(exclude)scripts/release-android.sh' \
    ':(exclude)scripts/check-sbom.py' \
    ':(exclude)*.md' \
    ':(exclude)packaging/sbom.cdx.json' \
    ':(exclude)packaging/rpm/clambhook.spec' \
    ':(exclude)debian/changelog'; then
    fail "active build, packaging, or UI files still reference a retired user-interface stack"
fi
grep -Fq 'id("org.jetbrains.compose")' ui/kotlin/shared/build.gradle.kts ||
    fail "the shared Kotlin UI is not built with Compose Multiplatform"

for binary in build-native/clambhook build-native/clambhook-tui build-native/clambhook-license; do
    [[ -f "$binary" ]] || continue
    if command -v readelf >/dev/null 2>&1 && readelf -S "$binary" 2>/dev/null | grep -q '\.go\.buildinfo'; then
        fail "retired runtime build information found in $binary"
    fi
done

if ! grep -Fq 'linux-dist/clambhook-ui' ui/kotlin/desktop/build.gradle.kts ||
    [[ ! -x ui/kotlin/desktop/src/linux/clambhook-ui ]]; then
    fail "desktop distributable name no longer matches the production Linux executable"
fi
grep -Fq 'applicationId = "org.jpfchang.clambhook"' ui/kotlin/app/build.gradle.kts ||
    fail "the Android application ID changed"

"$ROOT_DIR/scripts/check-android-abi-policy.sh"

echo "cutover check: all checks passed"
