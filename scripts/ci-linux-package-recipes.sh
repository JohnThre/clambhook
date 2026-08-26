#!/usr/bin/env bash
# Build real Linux package recipes inside throwaway Linux containers (Apple
# `container` on macOS, podman/docker on Linux). Outputs remain inside the
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
        tar --exclude-vcs --exclude='./dist' \
            --transform "s,^\.,clambhook-${version}," \
            -czf "$source" .
        rpmbuild --define "_topdir $topdir" --define "version $version" \
            -bb packaging/rpm/clambhook.spec
    )

    compgen -G "$topdir/RPMS/*/clambhook-*.rpm" >/dev/null || {
        echo "RPM recipe completed without producing a package." >&2
        return 1
    }
    log "RPM recipe built and verified in the temporary container workspace"
}

case "$MODE" in
debian) build_debian ;;
rpm) build_rpm ;;
*)
    echo "usage: scripts/ci-linux-package-recipes.sh debian|rpm" >&2
    exit 2
    ;;
esac
