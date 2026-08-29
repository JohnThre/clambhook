#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Sign Linux package checksums and the final manifest after isolated container
# builds. The private key remains on the protected runner, never in a container.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${1:-$ROOT_DIR/dist/linux}"
GPG_KEY="${GPG_KEY:-EAA876B70B1832F5}"

[[ -d "$DIST_DIR" ]] || {
  echo "Linux release directory not found: $DIST_DIR" >&2
  exit 1
}
command -v gpg >/dev/null 2>&1 || {
  echo "gpg is required for Linux release signing." >&2
  exit 2
}

passphrase_args=()
if [[ -n "${GPG_PASSPHRASE_FILE:-}" ]]; then
  [[ -f "$GPG_PASSPHRASE_FILE" ]] || {
    echo "GPG_PASSPHRASE_FILE does not exist." >&2
    exit 2
  }
  passphrase_args=(--passphrase-file "$GPG_PASSPHRASE_FILE")
fi

shopt -s nullglob
targets=("$DIST_DIR"/*.sha256 "$DIST_DIR"/clambhook-linux-*-manifest.json)
[[ ${#targets[@]} -gt 1 ]] || {
  echo "Linux release checksums or manifest are missing." >&2
  exit 1
}

for target in "${targets[@]}"; do
  gpg --batch --yes --pinentry-mode loopback \
    "${passphrase_args[@]}" \
    --local-user "$GPG_KEY" \
    --detach-sign --armor --output "$target.sig" "$target"
  echo "GPG-signed $target"
done
