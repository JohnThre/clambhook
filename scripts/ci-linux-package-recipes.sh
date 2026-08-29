#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Build real Linux package recipes inside throwaway Podman/Docker containers.
# Outputs remain inside the
# throwaway container and are never uploaded.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="${1:-}"
WORKDIR_TO_CLEAN=""

cleanup() {
    if [[ -n "$WORKDIR_TO_CLEAN" ]]; then
        rm -rf "$WORKDIR_TO_CLEAN"
    fi
}
trap cleanup EXIT

log() {
    printf 'ci-package-recipes: %s\n' "$*"
}

build_debian() {
    PACKAGE_SMOKE_TARGETS=debian \
        PACKAGE_SMOKE_REQUIRE_TOOLS=1 \
        PACKAGE_SMOKE_INSTALL=1 \
        "$ROOT_DIR/scripts/package-smoke.sh"
}

build_rpm() {
    command -v rpmbuild >/dev/null 2>&1 || {
        echo "rpmbuild is required for the RPM recipe." >&2
        return 2
    }

    local version="${CI_PACKAGE_VERSION:-0.0.0}"
    local workdir topdir source
    workdir="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-rpm-ci.XXXXXX")"
    WORKDIR_TO_CLEAN="$workdir"
    topdir="$workdir/rpmbuild"
    source="$topdir/SOURCES/clambhook-$version.tar.gz"
    mkdir -p "$topdir"/{BUILD,RPMS,SOURCES,SPECS,SRPMS}

    (
        cd "$ROOT_DIR"
        tar --exclude-vcs --exclude='./dist' --exclude='./build-native*' \
            --exclude='./ui/javafx/target' --exclude='./ui/android/.gradle' \
            --exclude='./ui/android/.native-deps' --exclude='./ui/android/app/build' \
            --transform "s,^\.,clambhook-${version}," \
            -czf "$source" .
        rpmbuild --define "_topdir $topdir" --define "version $version" \
            -bb packaging/rpm/clambhook.spec
    )

    compgen -G "$topdir/RPMS/*/clambhook-*.rpm" >/dev/null || {
        echo "RPM recipe completed without producing a package." >&2
        return 1
    }
    local package
    package="$(find "$topdir/RPMS" -type f -name 'clambhook-*.rpm' -print -quit)"
    CLAMBHOOK_CONTAINER_PACKAGE_SMOKE=1 \
        "$ROOT_DIR/scripts/smoke-installed-linux-package.sh" "$package"
    log "RPM recipe installed, exercised, uninstalled, and verified"
}

case "$MODE" in
debian) build_debian ;;
rpm) build_rpm ;;
*)
    echo "usage: scripts/ci-linux-package-recipes.sh debian|rpm" >&2
    exit 2
    ;;
esac
