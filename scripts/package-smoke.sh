#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat <<'USAGE'
Usage: scripts/package-smoke.sh [--strict]

Runs release packaging smoke checks. By default, checks that packaging metadata
exists, stages the shared install path under a temporary DESTDIR, and runs any
package-manager build checks whose toolchains are available.

Options:
  --strict    Fail when optional packaging toolchains are missing and enable
              Homebrew formula install/test smoke.

Environment:
  PACKAGE_SMOKE_TARGETS          Space-separated targets to run.
                                 Default: paths install linux-gui homebrew debian rpm guix fdroid
  PACKAGE_SMOKE_VERSION          Version string used for staged install checks.
                                 Default: package-smoke
  PACKAGE_SMOKE_REQUIRE_TOOLS    If 1, missing optional packaging tools fail.
  PACKAGE_SMOKE_HOMEBREW_INSTALL If 1, run brew install/test for the formula.
USAGE
}

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOST_OS="$(uname -s 2>/dev/null || echo unknown)"
TARGETS="${PACKAGE_SMOKE_TARGETS:-paths install linux-gui homebrew debian rpm guix fdroid}"
SMOKE_VERSION="${PACKAGE_SMOKE_VERSION:-package-smoke}"
REQUIRE_TOOLS="${PACKAGE_SMOKE_REQUIRE_TOOLS:-0}"
HOMEBREW_INSTALL="${PACKAGE_SMOKE_HOMEBREW_INSTALL:-0}"

for arg in "$@"; do
    case "$arg" in
        --strict)
            REQUIRE_TOOLS=1
            HOMEBREW_INSTALL=1
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "unknown argument: $arg" >&2
            usage >&2
            exit 2
            ;;
    esac
done

WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-package-smoke.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

export GOCACHE="${GOCACHE:-$WORKDIR/go-build-cache}"
export GOMODCACHE="${GOMODCACHE:-$WORKDIR/go-mod-cache}"
mkdir -p "$GOCACHE" "$GOMODCACHE"

log() {
    printf 'package-smoke: %s\n' "$*"
}

want() {
    local target="$1"
    case " $TARGETS " in
        *" all "*|*" $target "*) return 0 ;;
        *) return 1 ;;
    esac
}

have() {
    command -v "$1" >/dev/null 2>&1
}

skip_or_fail() {
    local message="$1"
    if [ "$REQUIRE_TOOLS" = "1" ]; then
        echo "package-smoke: $message" >&2
        exit 2
    fi
    log "skip: $message"
}

need_tools() {
    local missing=()
    local tool
    for tool in "$@"; do
        if ! have "$tool"; then
            missing+=("$tool")
        fi
    done
    if [ "${#missing[@]}" -gt 0 ]; then
        skip_or_fail "missing tool(s): ${missing[*]}"
        return 1
    fi
}

require_linux_target() {
    local target="$1"
    if [ "$HOST_OS" != "Linux" ]; then
        skip_or_fail "$target smoke requires a Linux package build environment; current host is $HOST_OS"
        return 1
    fi
}

assert_file() {
    local path="$1"
    if [ ! -f "$path" ]; then
        echo "package-smoke: missing file: $path" >&2
        exit 1
    fi
}

assert_executable() {
    local path="$1"
    if [ ! -x "$path" ]; then
        echo "package-smoke: missing executable: $path" >&2
        exit 1
    fi
}

assert_version_output() {
    local bin="$1"
    local output
    output="$("$bin" -version 2>&1)"
    case "$output" in
        *"$SMOKE_VERSION"*) ;;
        *)
            echo "package-smoke: unexpected version output from $bin: $output" >&2
            exit 1
            ;;
    esac
}

smoke_installed_root() {
    local root="$1"
    local prefix="${2-/usr}"
    local bindir

    if [ -n "$prefix" ]; then
        bindir="$root$prefix/bin"
    else
        bindir="$root/bin"
    fi

    assert_executable "$bindir/clambhook"
    assert_executable "$bindir/clambhook-tui"
    assert_version_output "$bindir/clambhook"
    assert_version_output "$bindir/clambhook-tui"
}

smoke_installed_linux_gui() {
    local root="$1"
    local prefix="${2-/usr}"
    local base

    if [ -n "$prefix" ]; then
        base="$root$prefix"
    else
        base="$root"
    fi

    assert_executable "$base/bin/clambhook-linux"
    assert_executable "$base/libexec/clambhook"
    assert_file "$base/share/applications/com.clambhook.Clambhook.desktop"
    assert_file "$base/share/metainfo/com.clambhook.Clambhook.metainfo.xml"
    assert_file "$base/share/icons/hicolor/1024x1024/apps/com.clambhook.Clambhook.png"
}

prepare_source_tree() {
    local dest="$1"
    mkdir -p "$dest"

    if have rsync; then
        rsync -a \
            --exclude '/.git' \
            --exclude '/.worktrees' \
            --exclude '/bin' \
            --exclude '/ui/android/build' \
            --exclude '/ui/android/app/build' \
            --exclude '/ui/android/app/libs' \
            --exclude '/ui/linux/builddir' \
            "$ROOT"/ "$dest"/
        return
    fi

    (
        cd "$ROOT"
        tar -cf - \
            --exclude './.git' \
            --exclude './.worktrees' \
            --exclude './bin' \
            --exclude './ui/android/build' \
            --exclude './ui/android/app/build' \
            --exclude './ui/android/app/libs' \
            --exclude './ui/linux/builddir' \
            .
    ) | (
        cd "$dest"
        tar -xf -
    )
}

smoke_paths() {
    want paths || return 0
    log "checking packaging metadata paths"

    assert_file "$ROOT/packaging/homebrew/clambhook.rb"
    assert_file "$ROOT/packaging/rpm/clambhook.spec"
    assert_file "$ROOT/packaging/guix/clambhook.scm"
    assert_file "$ROOT/packaging/fdroid/README.md"
    assert_file "$ROOT/ui/linux/com.clambhook.Clambhook.yml"
    assert_file "$ROOT/ui/linux/meson_options.txt"
    assert_file "$ROOT/ui/linux/data/com.clambhook.Clambhook.desktop.in"
    assert_file "$ROOT/ui/linux/data/com.clambhook.Clambhook.metainfo.xml.in"
    assert_file "$ROOT/clambhook-icon-1024.png"
    assert_file "$ROOT/debian/control"
    assert_file "$ROOT/debian/copyright"
    assert_file "$ROOT/debian/rules"
    assert_file "$ROOT/debian/source/format"
    assert_file "$ROOT/debian/changelog"
    assert_file "$ROOT/ui/android/app/build.gradle.kts"
    assert_file "$ROOT/ui/android/app/src/main/AndroidManifest.xml"
}

smoke_linux_gui_install() {
    want linux-gui || return 0
    log "staging Linux GUI install under temporary DESTDIR"

    require_linux_target "Linux GUI install" || return 0
    need_tools go meson valac pkg-config || return 0
    if ! pkg-config --exists gtk4 libadwaita-1 gee-0.8 json-glib-1.0 libsoup-3.0 libsecret-1; then
        skip_or_fail "missing GTK/libadwaita development pkg-config dependencies"
        return 0
    fi

    local root="$WORKDIR/linux-gui-root"
    mkdir -p "$root"
    (cd "$ROOT" && make install-linux DESTDIR="$root" PREFIX=/usr VERSION="$SMOKE_VERSION")
    smoke_installed_linux_gui "$root" /usr
    assert_version_output "$root/usr/libexec/clambhook"
}

smoke_make_install() {
    want install || return 0
    log "staging make install under temporary DESTDIR"

    local root="$WORKDIR/install-root"
    mkdir -p "$root"
    (cd "$ROOT" && make install DESTDIR="$root" PREFIX=/usr VERSION="$SMOKE_VERSION")
    smoke_installed_root "$root" /usr
}

smoke_homebrew() {
    want homebrew || return 0
    log "checking Homebrew formula path"

    local formula="$ROOT/packaging/homebrew/clambhook.rb"
    assert_file "$formula"

    if [ "$HOMEBREW_INSTALL" != "1" ]; then
        log "skip: Homebrew install smoke disabled; set PACKAGE_SMOKE_HOMEBREW_INSTALL=1 or pass --strict"
        return 0
    fi
    need_tools brew || return 0

    brew install --build-from-source --formula "$formula"
    brew test "$formula"
}

smoke_debian() {
    want debian || return 0
    log "building Debian package and extracting payload"

    require_linux_target Debian || return 0
    need_tools dpkg-buildpackage dpkg-deb || return 0

    local src_parent="$WORKDIR/debian-src"
    local src="$src_parent/clambhook"
    local root="$WORKDIR/debian-root"
    prepare_source_tree "$src"
    mkdir -p "$root"

    (cd "$src" && dpkg-buildpackage -us -uc -b)

    local deb
    deb="$(find "$src_parent" -maxdepth 1 -name 'clambhook_*.deb' -print -quit)"
    if [ -z "$deb" ]; then
        echo "package-smoke: Debian package was not produced" >&2
        exit 1
    fi
    dpkg-deb -x "$deb" "$root"
    smoke_installed_root "$root" /usr
    smoke_installed_linux_gui "$root" /usr
}

smoke_rpm() {
    want rpm || return 0
    log "building RPM package and extracting payload"

    require_linux_target RPM || return 0
    need_tools rpmbuild rpm2cpio cpio tar || return 0

    local spec="$ROOT/packaging/rpm/clambhook.spec"
    local version
    version="$(awk '/^Version:/ { print $2; exit }' "$spec")"
    if [ -z "$version" ]; then
        echo "package-smoke: unable to read RPM Version from $spec" >&2
        exit 1
    fi

    local src_parent="$WORKDIR/rpm-src"
    local src="$src_parent/clambhook-$version"
    local topdir="$WORKDIR/rpmbuild"
    local root="$WORKDIR/rpm-root"
    prepare_source_tree "$src"
    mkdir -p "$topdir/SOURCES" "$topdir/BUILD" "$topdir/BUILDROOT" "$topdir/RPMS" "$topdir/SRPMS" "$root"

    tar -czf "$topdir/SOURCES/clambhook-$version.tar.gz" -C "$src_parent" "clambhook-$version"
    rpmbuild --nodeps --define "_topdir $topdir" -bb "$src/packaging/rpm/clambhook.spec"

    local rpm
    rpm="$(find "$topdir/RPMS" -type f -name 'clambhook-*.rpm' -print -quit)"
    if [ -z "$rpm" ]; then
        echo "package-smoke: RPM package was not produced" >&2
        exit 1
    fi
    (cd "$root" && rpm2cpio "$rpm" | cpio -idm --quiet)
    smoke_installed_root "$root" /usr
    smoke_installed_linux_gui "$root" /usr
}

smoke_guix() {
    want guix || return 0
    log "building Guix package definition"

    require_linux_target Guix || return 0
    need_tools guix || return 0

    local output
    local store_path
    output="$(guix build -f "$ROOT/packaging/guix/clambhook.scm")"
    store_path="$(printf '%s\n' "$output" | tail -n 1)"
    if [ -z "$store_path" ]; then
        echo "package-smoke: Guix build did not return a store path" >&2
        exit 1
    fi
    smoke_installed_root "$store_path" ""
    smoke_installed_linux_gui "$store_path" ""
}

smoke_flatpak() {
    want flatpak || return 0
    log "building Linux Flatpak bundle"

    require_linux_target Flatpak || return 0
    need_tools flatpak flatpak-builder || return 0

    (cd "$ROOT" && make test-linux-flatpak)
    assert_file "$ROOT/dist/linux/com.clambhook.Clambhook.flatpak"
}

smoke_fdroid() {
    want fdroid || return 0
    log "building Android release package for F-Droid path"

    assert_file "$ROOT/packaging/fdroid/README.md"
    assert_file "$ROOT/ui/android/app/build.gradle.kts"
    assert_file "$ROOT/ui/android/app/src/main/AndroidManifest.xml"

    if [ -z "${ANDROID_HOME:-}" ] && [ -n "${ANDROID_SDK_ROOT:-}" ]; then
        export ANDROID_HOME="$ANDROID_SDK_ROOT"
    fi
    if [ -z "${ANDROID_HOME:-}${ANDROID_SDK_ROOT:-}" ]; then
        skip_or_fail "ANDROID_HOME or ANDROID_SDK_ROOT is required for Android/F-Droid smoke"
        return 0
    fi
    need_tools go gomobile gobind || return 0

    (cd "$ROOT" && make build-android-release)

    local apk
    apk="$(find "$ROOT/ui/android/app/build/outputs/apk/release" -type f -name '*.apk' -print -quit)"
    if [ -z "$apk" ]; then
        echo "package-smoke: Android release APK was not produced" >&2
        exit 1
    fi
}

smoke_paths
smoke_make_install
smoke_linux_gui_install
smoke_homebrew
smoke_debian
smoke_rpm
smoke_guix
smoke_flatpak
smoke_fdroid

log "completed"
