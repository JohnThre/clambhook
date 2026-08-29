#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ANDROID_DIR="$ROOT_DIR/ui/android"
LIBS_DIR="$ROOT_DIR/ui/javafx/src/android/libs"
AAR="$ANDROID_DIR/app/build/outputs/aar/clambhook-android-platform-release.aar"

(cd "$ANDROID_DIR" && ./gradlew --no-daemon :platform:assembleRelease)
[[ -f "$AAR" ]] || {
    echo "Android platform AAR not found: $AAR" >&2
    exit 1
}

install -d "$LIBS_DIR"
install -m 0644 "$AAR" "$LIBS_DIR/clambhook-android-platform-release.aar"
echo "Prepared Gluon Android platform library: $LIBS_DIR/clambhook-android-platform-release.aar"
