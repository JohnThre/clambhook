#!/usr/bin/env bash
# Build real Linux package recipes inside installer-validation containers.
# Outputs remain inside the throwaway job and are never uploaded.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="${1:-}"

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
    trap "rm -rf '$workdir'" EXIT
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

is_container_limit() {
    grep -Eiq 'fuse|fusermount|operation not permitted|permission denied.*namespace|user namespace|bwrap.*permission|cannot mount|failed to mount' "$1"
}

best_effort_recipe() {
    local name="$1"
    shift
    local output status
    output="$(mktemp "${TMPDIR:-/tmp}/clambhook-${name}.XXXXXX.log")"
    if "$@" > >(tee "$output") 2> >(tee -a "$output" >&2); then
        rm -f "$output"
        log "$name recipe built successfully"
        return 0
    else
        status=$?
    fi
    if is_container_limit "$output"; then
        log "SKIP: $name recipe reached a container FUSE/mount/namespace limitation; the required native portable job remains authoritative"
        rm -f "$output"
        return 0
    fi
    echo "ci-package-recipes: $name recipe failed for a reason other than a documented container limitation" >&2
    rm -f "$output"
    return "$status"
}

build_portable_probe() {
    best_effort_recipe flatpak \
        "$ROOT_DIR/scripts/ci-linux-portable-recipes.sh" flatpak
    best_effort_recipe appimage \
        "$ROOT_DIR/scripts/ci-linux-portable-recipes.sh" appimage
}

case "$MODE" in
    debian) build_debian ;;
    rpm) build_rpm ;;
    portable) build_portable_probe ;;
    *)
        echo "usage: scripts/ci-linux-package-recipes.sh debian|rpm|portable" >&2
        exit 2
        ;;
esac
