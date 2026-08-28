#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Static security and publication-policy checks for GitHub Actions workflows.
set -euo pipefail

ROOT_DIR="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
WORKFLOW_DIR="$ROOT_DIR/.github/workflows"
RELEASE_PUBLIC_KEY="$ROOT_DIR/keys/clambhook-release-key.asc"
EXPECTED_RELEASE_FINGERPRINT="BAFC7769FDA1E0D4EBD23E2F6FF4807EAD977A9B"

fail() {
  echo "github-actions-policy: $1" >&2
  exit 1
}

[[ -d "$WORKFLOW_DIR" ]] || fail "missing .github/workflows"
find "$WORKFLOW_DIR" -maxdepth 1 -type f -name '*.yml' -print -quit | grep -q . || \
  fail "no workflow files found"

[[ -f "$RELEASE_PUBLIC_KEY" ]] || fail "missing pinned public release key"
command -v gpg >/dev/null 2>&1 || fail "gpg is required to validate the public release key"
key_home="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-release-key.XXXXXX")"
chmod 0700 "$key_home"
fingerprint="$(GNUPGHOME="$key_home" gpg --batch --show-keys --with-colons \
  --fingerprint "$RELEASE_PUBLIC_KEY" 2>/dev/null | \
  awk -F: '$1 == "fpr" { print $10; exit }')"
rm -rf "$key_home"
[[ "$fingerprint" == "$EXPECTED_RELEASE_FINGERPRINT" ]] || \
  fail "public release key fingerprint does not match the pinned owner key"

if grep -Riq --include='*.yml' 'pull_request_target:' "$WORKFLOW_DIR"; then
  fail "pull_request_target is prohibited"
fi

while IFS=: read -r file line content; do
  uses="${content#*uses:}"
  uses="${uses%%#*}"
  uses="${uses//[[:space:]]/}"
  case "$uses" in
    ./*) continue ;;
  esac
  ref="${uses##*@}"
  if [[ ! "$ref" =~ ^[0-9a-f]{40}$ ]]; then
    fail "$file:$line action is not pinned to a full commit SHA: $uses"
  fi
done < <(grep -RHnE --include='*.yml' '^[[:space:]]*-?[[:space:]]*uses:' "$WORKFLOW_DIR" || true)

while IFS= read -r workflow; do
  grep -q '^permissions: {}$' "$workflow" || \
    fail "$workflow must default to permissions: {}"
done < <(find "$WORKFLOW_DIR" -maxdepth 1 -type f -name '*.yml' -print | sort)

# macOS application tests are always local-only. The protected release workflow
# may archive/sign the already locally validated app for publication, and
# Security may trace ClambhookShared without building ClambhookMac.
if grep -RiqE --include='*.yml' \
  '(make[[:space:]]+test-apple|swift[[:space:]]+test)' \
  "$WORKFLOW_DIR"; then
  fail "macOS app tests are prohibited in GitHub workflows"
fi

if grep -RiqE --include='*.yml' --exclude='release.yml' \
  '(make[[:space:]]+build-apple|xcodebuild[^[:cntrl:]]*ClambhookMac)' \
  "$WORKFLOW_DIR"; then
  fail "macOS app builds are prohibited outside release.yml"
fi

# Reports and logs may use Actions artifacts. Installer/package outputs and the
# entire dist tree may only go to the approved R2 deployment scripts.
if grep -RiqE --include='*.yml' \
  '(^|[[:space:]/])(dist(/|$)|[^[:space:]]+\.(apk|aab|dmg|pkg|deb|rpm|flatpak|AppImage))' \
  "$WORKFLOW_DIR"; then
  fail "workflow references an installer/package artifact or dist/ path"
fi

"$ROOT_DIR/scripts/check-source-only.sh" "$ROOT_DIR"
echo "GitHub Actions policy check passed."
