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
ALL_SECTIONS=(native javafx android apple linux smoke)

have() { command -v "$1" >/dev/null 2>&1; }

usage() {
    printf 'Usage: %s [native|javafx|android|apple|linux|smoke|all ...]\n' "$0"
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

section_javafx() {
    if ! have java || ! have mvn; then
        echo "ci-local: [javafx] skip: JDK 17 and Maven are required" >&2
        return "$SKIP_RC"
    fi
    echo "==================== ci-local: javafx ===================="
    make test-javafx
}

section_android() {
    if ! have java || [[ ! -x ui/android/gradlew ]]; then
        echo "ci-local: [android] skip: JDK 17 or Android Gradle wrapper is missing" >&2
        return "$SKIP_RC"
    fi
    echo "==================== ci-local: android ===================="
    make test-android
    if [[ -n "${GRAALVM_HOME:-}" ]]; then
        make build-android
    else
        echo "ci-local: [android] skip: Gluon image build needs GRAALVM_HOME" >&2
    fi
    if have android && [[ -n "${CI_LOCAL_ANDROID_AVD:-}" ]]; then
        android emulator start "$CI_LOCAL_ANDROID_AVD"
        make run-android
    else
        echo "ci-local: [android] skip: optional AVD journey needs android CLI and CI_LOCAL_ANDROID_AVD" >&2
    fi
}

section_apple() {
    if [[ "$HOST_OS" != "Darwin" ]] || ! have xcodebuild || ! have xcodegen; then
        echo "ci-local: [apple] skip: macOS, Xcode, and XcodeGen are required" >&2
        return "$SKIP_RC"
    fi
    echo "==================== ci-local: apple ===================="
    make build-apple
    make test-apple
}

section_linux() {
    [[ "$HOST_OS" == "Linux" ]] || {
        echo "ci-local: [linux] skip: GNU/Linux host required for Gluon native image" >&2
        return "$SKIP_RC"
    }
    if [[ -z "${GRAALVM_HOME:-}" ]] || ! have mvn; then
        echo "ci-local: [linux] skip: GRAALVM_HOME and Maven are required" >&2
        return "$SKIP_RC"
    fi
    echo "==================== ci-local: linux ===================="
    make build-linux
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
    if [[ "$HOST_OS" == "Darwin" ]]; then
        make macos-release-contract-check
    fi
    if [[ "$HOST_OS" == "Linux" && -n "${GRAALVM_HOME:-}" ]]; then
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
        native|javafx|android|apple|linux|smoke) sections+=("$arg") ;;
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
