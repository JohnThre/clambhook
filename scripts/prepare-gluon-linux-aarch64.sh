#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# GluonFX 1.0.29 unconditionally asks the linker for libgluon_drm on Linux
# AArch64. That library is Gluon's commercial framebuffer extension, while
# ClambHook's Ubuntu/Fedora desktop image uses the GTK/X11 backend exclusively.
#
# Create an empty archive only after Gluon has downloaded the pinned JavaFX
# static SDK. GNU ld accepts the archive as the requested library, but still
# fails if any DRM/Monocle symbol is actually reachable. The subsequent Xvfb
# launch in the distro harness proves that the GTK desktop path is runnable.
set -euo pipefail

static_version="${1:-}"
if [[ ! "$static_version" =~ ^[0-9A-Za-z.+-]+$ ]]; then
    echo "usage: scripts/prepare-gluon-linux-aarch64.sh JAVAFX_STATIC_VERSION" >&2
    exit 2
fi

case "$(uname -s)-$(uname -m)" in
    Linux-aarch64|Linux-arm64) ;;
    *)
        echo "the Gluon Linux AArch64 link guard must run on Linux AArch64" >&2
        exit 2
        ;;
esac

user_home="${HOME:?HOME is required to locate the Gluon cache}"
sdk_lib="$user_home/.gluon/substrate/javafxStaticSdk/$static_version/linux-aarch64/sdk/lib"

for required in libglass.a libglassgtk3.a libglass_monocle.a; do
    if [[ ! -f "$sdk_lib/$required" ]]; then
        echo "Gluon JavaFX static SDK is incomplete: $sdk_lib/$required" >&2
        echo "Run gluonfx:compile before preparing the Linux AArch64 link." >&2
        exit 2
    fi
done

archive="$sdk_lib/libgluon_drm.a"
if [[ -e "$archive" ]]; then
    if [[ ! -f "$archive" ]] || [[ "$(wc -c < "$archive")" -ne 8 ]] ||
        ! printf '!<arch>\n' | cmp -s - "$archive"; then
        echo "refusing to replace an existing Gluon DRM library: $archive" >&2
        echo "Use an isolated build account/cache for the GTK-only package build." >&2
        exit 2
    fi
    exit 0
fi

umask 022
printf '!<arch>\n' > "$archive"
printf 'Prepared GTK-only Linux AArch64 link guard: %s\n' "$archive"
