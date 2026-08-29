#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# GluonFX 1.0.29 treats every Linux AArch64 target as a Raspberry Pi/Monocle
# target. ClambHook instead builds native Ubuntu/Fedora GTK desktops. Prepare an
# isolated Maven repository whose checksum-pinned Substrate artifact changes
# only that class-local backend selector, and pair it with Gluon's official
# non-Monocle JavaFX static SDK. The AArch64 target triplet is unchanged.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
static_version="${1:-}"
build_dir="${2:-}"
if [[ ! "$static_version" =~ ^[0-9A-Za-z.+-]+$ ]] ||
    [[ -z "$build_dir" || "$build_dir" != /* || "$build_dir" == "/" ]]; then
    echo "usage: scripts/prepare-gluon-linux-aarch64.sh JAVAFX_STATIC_VERSION /absolute/build/directory" >&2
    exit 2
fi

case "$(uname -s)-$(uname -m)" in
    Linux-aarch64|Linux-arm64) ;;
    *)
        echo "the Gluon Linux AArch64 GTK preparation must run on Linux AArch64" >&2
        exit 2
        ;;
esac

for command_name in curl python3; do
    command -v "$command_name" >/dev/null 2>&1 || {
        echo "$command_name is required for the Linux AArch64 Gluon build" >&2
        exit 2
    }
done

graalvm_home="${GRAALVM_HOME:?GRAALVM_HOME must point to GraalVM Community 17}"
[[ -d "$graalvm_home" && "$graalvm_home" != "/" ]] || {
    echo "invalid GRAALVM_HOME: $graalvm_home" >&2
    exit 2
}

static_sha256="44beff405df3719f597e046cbdcd8f8ec245c4813ad3d0f5418e6ab50992231b"
substrate_jar_sha256="d2ba5f26578e4aa81e358f2e9fdf107c1d528294920db4e4a70841a678e49cf4"
substrate_pom_sha256="40365f7737cceb4561ef0a586e3f6a54b7e5fc98b2df604c0d88bff3209b72d1"
substrate_version="0.0.69"
static_name="openjfx-${static_version}-linux-aarch64-static.zip"
static_url="https://download2.gluonhq.com/substrate/javafxstaticsdk/$static_name"
substrate_base="https://repo.maven.apache.org/maven2/com/gluonhq/substrate/$substrate_version"

downloads="$build_dir/downloads"
sdk_path="$build_dir/javafx-static-sdk"
maven_artifact="$build_dir/m2/com/gluonhq/substrate/$substrate_version"
mkdir -p "$downloads" "$maven_artifact"

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d ' ' -f 1
    else
        shasum -a 256 "$1" | cut -d ' ' -f 1
    fi
}

download_verified() {
    local url="$1" expected="$2" destination="$3" actual temporary
    if [[ -f "$destination" ]]; then
        actual="$(sha256_file "$destination")"
        if [[ "$actual" == "$expected" ]]; then
            return
        fi
        echo "refusing stale or modified cached download: $destination" >&2
        exit 2
    fi
    temporary="$(mktemp "$downloads/.download.XXXXXX")"
    curl --fail --location --silent --show-error --retry 5 \
        --output "$temporary" "$url"
    actual="$(sha256_file "$temporary")"
    if [[ "$actual" != "$expected" ]]; then
        echo "SHA-256 mismatch for $url" >&2
        echo "expected $expected, got $actual" >&2
        rm -f "$temporary"
        exit 2
    fi
    mv "$temporary" "$destination"
}

static_archive="$downloads/$static_name"
substrate_jar="$downloads/substrate-$substrate_version.jar"
substrate_pom="$downloads/substrate-$substrate_version.pom"
download_verified "$static_url" "$static_sha256" "$static_archive"
download_verified "$substrate_base/substrate-$substrate_version.jar" \
    "$substrate_jar_sha256" "$substrate_jar"
download_verified "$substrate_base/substrate-$substrate_version.pom" \
    "$substrate_pom_sha256" "$substrate_pom"

sdk_marker="$sdk_path/.clambhook-source-sha256"
if [[ -d "$sdk_path" ]]; then
    [[ -f "$sdk_marker" && "$(< "$sdk_marker")" == "$static_sha256" ]] || {
        echo "refusing an unverified JavaFX static SDK directory: $sdk_path" >&2
        echo "Run make clean before retrying." >&2
        exit 2
    }
else
    extraction="$(mktemp -d "$build_dir/.javafx-static.XXXXXX")"
    trap 'rm -rf "$extraction"' EXIT
    python3 -m zipfile -e "$static_archive" "$extraction"
    [[ -d "$extraction/sdk" ]] || {
        echo "official JavaFX archive has no sdk directory" >&2
        exit 2
    }
    printf '%s\n' "$static_sha256" > "$extraction/sdk/.clambhook-source-sha256"
    mv "$extraction/sdk" "$sdk_path"
    rmdir "$extraction"
    trap - EXIT
fi

for required in \
    javafx.base.jar javafx.controls.jar javafx.graphics.jar \
    libglass.a libglassgtk3.a libprism_es2.a; do
    [[ -f "$sdk_path/lib/$required" ]] || {
        echo "official GTK JavaFX static SDK is incomplete: $sdk_path/lib/$required" >&2
        exit 2
    }
done
for forbidden in libglass_monocle.a libprism_es2_monocle.a libgluon_drm.a; do
    [[ ! -e "$sdk_path/lib/$forbidden" ]] || {
        echo "unexpected Monocle/DRM library in GTK JavaFX SDK: $forbidden" >&2
        exit 2
    }
done

python3 "$repo_root/scripts/patch-gluon-substrate-aarch64.py" "$substrate_jar" \
    "$maven_artifact/substrate-$substrate_version.jar"
install -m 0644 "$substrate_pom" "$maven_artifact/substrate-$substrate_version.pom"

# Substrate's Java 17 triplet uses a versioned clibrary location. Community
# GraalVM 17.0.9 ships the matching libraries in its unversioned directory.
# Copy only the two archives Substrate links, and never replace differing data.
source_clibs="$graalvm_home/lib/svm/clibraries/linux-aarch64"
target_clibs="$graalvm_home/lib/svm/clibraries/27/linux-aarch64"
mkdir -p "$target_clibs"
for library in libjvm.a liblibchelper.a; do
    source="$source_clibs/$library"
    target="$target_clibs/$library"
    [[ -f "$source" ]] || {
        echo "GraalVM Community AArch64 clibrary is missing: $source" >&2
        exit 2
    }
    if [[ -e "$target" && ! -f "$target" ]]; then
        echo "refusing non-file GraalVM clibrary target: $target" >&2
        exit 2
    fi
    if [[ -f "$target" ]] && [[ ! "$source" -ef "$target" ]] &&
        ! cmp -s "$source" "$target"; then
        echo "refusing to replace a different GraalVM clibrary: $target" >&2
        exit 2
    fi
    install -m 0644 "$source" "$target"
done

printf 'Prepared deterministic Linux AArch64 GTK toolchain in %s\n' "$build_dir"
