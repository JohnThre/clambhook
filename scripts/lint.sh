#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="${CLAMBHOOK_LINT_BUILD_DIR:-$ROOT_DIR/build-native-lint}"
cd "$ROOT_DIR"

scripts/check-license-policy.sh
scripts/check-cutover.sh

if command -v shellcheck >/dev/null 2>&1; then
    shell_files=()
    while IFS= read -r script; do
        [[ -f "$script" ]] && shell_files+=("$script")
    done < <(git ls-files '*.sh')
    shellcheck -x "${shell_files[@]}"
else
    echo "lint: shellcheck is unavailable; running parser checks only" >&2
    while IFS= read -r script; do
        bash -n "$script"
    done < <(git ls-files '*.sh')
fi

cmake -S . -B "$BUILD_DIR" -G Ninja \
    -DCMAKE_BUILD_TYPE=Debug \
    -DCLAMBHOOK_ENABLE_SANITIZERS=OFF \
    -DCLAMBHOOK_WARNINGS_AS_ERRORS=ON
cmake --build "$BUILD_DIR"

(cd ui/kotlin && ./gradlew --no-daemon :desktop:compileKotlin)
if (cd ui/kotlin && ./gradlew --no-daemon -q projects | grep -q "':platform'"); then
    (cd ui/kotlin && ./gradlew --no-daemon :platform:lintDebug :app:lintDebug)
else
    echo "lint: Android lint skipped: no Android SDK configured" >&2
fi

echo "lint: all checks passed"
