#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Import an owner-held release key into an isolated CI GnuPG home without
# printing key material or passphrases. Writes safe path variables to the
# GitHub Actions environment file supplied as the second argument.
set -euo pipefail

TEMP_ROOT="${1:?temporary root is required}"
ENV_FILE="${2:?GitHub environment file is required}"
# shellcheck source=scripts/lib/release-gpg.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/scripts/lib/release-gpg.sh"
EXPECTED_PRIMARY_FINGERPRINT="$CLAMBHOOK_GPG_PRIMARY_FPR"
EXPECTED_SIGNING_FINGERPRINT="$CLAMBHOOK_GPG_SIGNING_FPR"

[[ -n "${GPG_PRIVATE_KEY_BASE64:-}" ]] || {
  echo "GPG_PRIVATE_KEY_BASE64 is required." >&2
  exit 2
}
command -v gpg >/dev/null 2>&1 || {
  echo "gpg is required." >&2
  exit 2
}
command -v openssl >/dev/null 2>&1 || {
  echo "openssl is required." >&2
  exit 2
}

gnupg_home="$TEMP_ROOT/clambhook-gnupg"
private_key="$TEMP_ROOT/clambhook-release-key"
passphrase_file="$TEMP_ROOT/clambhook-gpg-passphrase"
mkdir -p "$gnupg_home"
chmod 0700 "$gnupg_home"

printf '%s' "$GPG_PRIVATE_KEY_BASE64" | openssl base64 -d -A > "$private_key"
chmod 0600 "$private_key"
gpg --batch --homedir "$gnupg_home" --import "$private_key"
rm -f "$private_key"

printf '%s' "${GPG_PASSPHRASE:-}" > "$passphrase_file"
chmod 0600 "$passphrase_file"

fingerprints="$(gpg --batch --homedir "$gnupg_home" --with-colons \
  --with-subkey-fingerprints --fingerprint --list-secret-keys "$GPG_KEY" | \
  awk -F: '$1 == "fpr" { print $10 }')"
[[ "$(printf '%s\n' "$fingerprints" | sed -n '1p')" == "$EXPECTED_PRIMARY_FINGERPRINT" ]] || {
  echo "Configured GPG secret key does not match the ClambHook release key." >&2
  exit 2
}
printf '%s\n' "$fingerprints" | grep -qx "$EXPECTED_SIGNING_FINGERPRINT" || {
  echo "Configured GPG secret key is missing the ClambHook signing subkey." >&2
  exit 2
}
{
  printf 'GNUPGHOME=%s\n' "$gnupg_home"
  printf 'GPG_KEY=%s\n' "$GPG_KEY"
  printf 'CLAMBHOOK_GPG_KEY=%s\n' "$GPG_KEY"
  printf 'GPG_PASSPHRASE_FILE=%s\n' "$passphrase_file"
} >> "$ENV_FILE"

echo "Imported the configured release key into an isolated CI keyring."
