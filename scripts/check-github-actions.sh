#!/usr/bin/env bash
# Static security and publication-policy checks for GitHub Actions workflows.
set -euo pipefail

ROOT_DIR="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
WORKFLOW_DIR="$ROOT_DIR/.github/workflows"

fail() {
  echo "github-actions-policy: $1" >&2
  exit 1
}

[[ -d "$WORKFLOW_DIR" ]] || fail "missing .github/workflows"
find "$WORKFLOW_DIR" -maxdepth 1 -type f -name '*.yml' -print -quit | grep -q . || \
  fail "no workflow files found"

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

# Reports and logs may use Actions artifacts. Installer/package outputs and the
# entire dist tree may only go to the approved R2 deployment scripts.
if grep -RiqE --include='*.yml' \
  '(^|[[:space:]/])(dist(/|$)|[^[:space:]]+\.(apk|aab|dmg|pkg|deb|rpm|flatpak|AppImage))' \
  "$WORKFLOW_DIR"; then
  fail "workflow references an installer/package artifact or dist/ path"
fi

"$ROOT_DIR/scripts/check-source-only.sh" "$ROOT_DIR"
echo "GitHub Actions policy check passed."
