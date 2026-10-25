#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Local mirror of the portable CI gates. Distro containers and Android
# managed devices remain optional locally; their hosted lanes are authoritative.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

HOST_OS="$(uname -s 2>/dev/null || echo unknown)"
SKIP_RC=200
ALL_SECTIONS=(native linux smoke)

have() { command -v "$1" >/dev/null 2>&1; }

usage() {
    printf 'Usage: %s [native|linux|smoke|all ...]\n' "$0"
    exit 0
}

section_native() {
    if ! have cmake || ! have ninja || ! have pkg-config; then
        echo "ci-local: [native] skip: CMake, Ninja, and pkg-config are required" >&2
        return "$SKIP_RC"
    fi
    echo "==================== ci-local: native ===================="
    make test-native
    make lint
}

section_linux() {
    [[ "$HOST_OS" == "Linux" ]] || {
        echo "ci-local: [linux] skip: an Ubuntu or Fedora host is required" >&2
        return "$SKIP_RC"
    }
    echo "==================== ci-local: linux ===================="
    if have podman || have docker; then
        scripts/validate-linux-distros.sh
    else
        echo "ci-local: [linux] skip: optional distro lanes need Podman or Docker" >&2
    fi
}

section_smoke() {
    echo "==================== ci-local: smoke ===================="
    scripts/check-cutover.sh
    scripts/check-license-policy.sh
    scripts/validate-systemd-unit.sh
    scripts/check-github-actions.sh
    if [[ "$HOST_OS" == "Linux" ]]; then
        make package-smoke
    else
        echo "ci-local: [smoke] skip: package smoke is authoritative on GNU/Linux" >&2
    fi
}

sections=()
for arg in "$@"; do
    case "$arg" in
        -h|--help) usage ;;
        all) sections=("${ALL_SECTIONS[@]}"); break ;;
        native|linux|smoke) sections+=("$arg") ;;
        *) echo "ci-local: unknown section '$arg'" >&2; exit 2 ;;
    esac
done
[[ ${#sections[@]} -gt 0 ]] || sections=("${ALL_SECTIONS[@]}")

results=()
failed=()
for section in "${sections[@]}"; do
    rc=0
    (set -e; "section_$section") || rc=$?
    if [[ "$rc" -eq 0 ]]; then
        results+=("$section:ok")
    elif [[ "$rc" -eq "$SKIP_RC" ]]; then
        results+=("$section:skip")
    else
        results+=("$section:FAIL($rc)")
        failed+=("$section")
    fi
done

printf 'ci-local: %s\n' "${results[*]}"
[[ ${#failed[@]} -eq 0 ]] || {
    echo "ci-local: failed sections: ${failed[*]}" >&2
    exit 1
}
