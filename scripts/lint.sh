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

(cd ui/javafx && mvn -B -DskipTests package)
(cd ui/android && ./gradlew --no-daemon :platform:lintDebug)

echo "lint: all checks passed"
