#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Decode the protected Android keystore into the runner temporary directory
# and export GluonFX signing inputs through the GitHub Actions environment file.
set -euo pipefail

TEMP_ROOT="${1:?temporary root is required}"
ENV_FILE="${2:?GitHub Actions environment file is required}"
KEYSTORE="$TEMP_ROOT/clambhook-android-release.jks"

for name in ANDROID_KEYSTORE_BASE64 ANDROID_KEYSTORE_PASSWORD ANDROID_KEY_ALIAS ANDROID_KEY_PASSWORD; do
    [[ -n "${!name:-}" ]] || { echo "$name is required." >&2; exit 2; }
done

for value in "$ANDROID_KEYSTORE_PASSWORD" "$ANDROID_KEY_ALIAS" "$ANDROID_KEY_PASSWORD"; do
    [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || {
        echo "Android signing values must not contain newlines." >&2
        exit 2
    }
done

mkdir -p "$TEMP_ROOT"
chmod 0700 "$TEMP_ROOT"
printf '%s' "$ANDROID_KEYSTORE_BASE64" | openssl base64 -d -A >"$KEYSTORE"
chmod 0600 "$KEYSTORE"

{
    printf 'CLAMBHOOK_ANDROID_KEYSTORE_PATH=%s\n' "$KEYSTORE"
    printf 'CLAMBHOOK_ANDROID_KEYSTORE_PASSWORD=%s\n' "$ANDROID_KEYSTORE_PASSWORD"
    printf 'CLAMBHOOK_ANDROID_KEY_ALIAS=%s\n' "$ANDROID_KEY_ALIAS"
    printf 'CLAMBHOOK_ANDROID_KEY_PASSWORD=%s\n' "$ANDROID_KEY_PASSWORD"
} >>"$ENV_FILE"

echo "Prepared protected Gluon Android signing configuration."
