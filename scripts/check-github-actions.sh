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

# Releases are built, signed, verified and published from the maintainer's
# machine with scripts/publish-release.sh. No workflow may build, sign or
# publish installers, and no workflow may hold release credentials.
[[ ! -e "$WORKFLOW_DIR/release.yml" ]] || \
  fail "release.yml returned; releases are published locally by scripts/publish-release.sh"
if grep -RiqE --include='*.yml' \
  '(^|[[:space:]/])(dist(/|$)|[^[:space:]]+\.(apk|aab|dmg|pkg|deb|rpm|flatpak|AppImage))' \
  "$WORKFLOW_DIR"; then
  fail "workflow references an installer/package artifact or dist/ path"
fi
if grep -RiqE --include='*.yml' \
  '(gh release (create|upload)|GPG_PRIVATE_KEY|APPLE_DEVELOPER_ID|ANDROID_KEYSTORE|SPARKLE_PRIVATE_KEY|environment:[[:space:]]*production)' \
  "$WORKFLOW_DIR"; then
  fail "workflow handles release publication or signing credentials"
fi

publish_script="$ROOT_DIR/scripts/publish-release.sh"
[[ -x "$publish_script" ]] || fail "missing executable scripts/publish-release.sh"
# Every installer upload must follow the developer@jpfchang.org signature
# verification gate.
verify_line="$(grep -n 'scripts/verify-release-signatures\.sh' "$publish_script" | head -1 | cut -d: -f1)"
upload_line="$(grep -n 'gh release upload' "$publish_script" | head -1 | cut -d: -f1)"
[[ -n "$verify_line" && -n "$upload_line" && "$verify_line" -lt "$upload_line" ]] || \
  fail "publish-release.sh uploads without a preceding signature verification gate"

"$ROOT_DIR/scripts/check-source-only.sh" "$ROOT_DIR"
echo "GitHub Actions policy check passed."
