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
    ':(exclude)scripts/check-cutover.sh' \
    ':(exclude)scripts/package-smoke.sh' \
    ':(exclude)scripts/smoke-installed-linux-package.sh'; then
    fail "active build, runtime, or documentation instructions still reference the retired implementation"
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

echo "cutover check: all checks passed"
