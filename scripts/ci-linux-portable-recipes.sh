#!/usr/bin/env bash
# Strict Flatpak and AppImage recipe validation for a native GNU/Linux CI host.
# Recipe failures are always fatal; constrained-container probes belong in
# ci-linux-package-recipes.sh and must not be used as required coverage.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="${1:-all}"

log() {
    printf 'ci-portable-recipes: %s\n' "$*"
}

require_command() {
    local command_name="$1"
    command -v "$command_name" >/dev/null 2>&1 || {
        echo "ci-portable-recipes: $command_name is required." >&2
        return 2
    }
}

build_flatpak() {
    require_command flatpak
    require_command flatpak-builder

    # flatpak-builder stores its state dir in the source tree root (.flatpak-builder)
    # and uses hardlinks between the state dir and the build dir. If they're on
    # different filesystems (e.g. /src bind-mount vs /tmp overlay), the build fails
    # with "not on the same filesystem". Use a workdir inside the source tree.
    local workdir
    workdir="$ROOT_DIR/.flatpak-ci-work"
    mkdir -p "$workdir"
    (
        trap 'rm -rf "$workdir"' EXIT
        export XDG_DATA_HOME="$workdir/data"
        export XDG_CACHE_HOME="$workdir/cache"
        export XDG_RUNTIME_DIR="$workdir/runtime"
        mkdir -p "$XDG_DATA_HOME" "$XDG_CACHE_HOME" "$XDG_RUNTIME_DIR"
        chmod 700 "$XDG_RUNTIME_DIR"

        flatpak remote-add --user --if-not-exists flathub \
            https://flathub.org/repo/flathub.flatpakrepo
        flatpak install --user --noninteractive -y flathub \
            org.freedesktop.Platform//24.08 \
            org.freedesktop.Sdk//24.08 \
            org.freedesktop.Sdk.Extension.openjdk17//24.08 \
            org.freedesktop.Sdk.Extension.golang//24.08
        flatpak-builder --user --force-clean \
            "$workdir/build" \
            "$ROOT_DIR/packaging/flatpak/com.clambhook.Clambhook.yaml"
    )
    log "Flatpak recipe built successfully"
}

build_appimage() {
    APPIMAGE_EXTRACT_AND_RUN=1 VERSION=ci \
        "$ROOT_DIR/packaging/appimage/build-appimage.sh"
    log "AppImage recipe built successfully"
}

case "$MODE" in
    flatpak) build_flatpak ;;
    appimage) build_appimage ;;
    all)
        build_flatpak
        build_appimage
        ;;
    *)
        echo "usage: scripts/ci-linux-portable-recipes.sh [all|flatpak|appimage]" >&2
        exit 2
        ;;
esac
