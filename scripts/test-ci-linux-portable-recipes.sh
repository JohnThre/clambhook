#!/usr/bin/env bash
# Focused contract test for the strict portable recipe CI entry point.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workdir="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-portable-script-test.XXXXXX")"
trap 'rm -rf "$workdir"' EXIT

mkdir -p "$workdir/repo/scripts" "$workdir/repo/packaging/appimage" \
    "$workdir/repo/packaging/flatpak" "$workdir/bin"
cp "$ROOT_DIR/scripts/ci-linux-portable-recipes.sh" "$workdir/repo/scripts/"
touch "$workdir/repo/packaging/flatpak/com.clambhook.Clambhook.yaml"

cat >"$workdir/bin/flatpak" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf 'flatpak %s\n' "$*" >> "$PORTABLE_TEST_CALLS"
MOCK
cat >"$workdir/bin/flatpak-builder" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf 'flatpak-builder %s\n' "$*" >> "$PORTABLE_TEST_CALLS"
if [[ "${PORTABLE_TEST_FLATPAK_FAIL:-0}" == "1" ]]; then
    echo 'operation not permitted: user namespace' >&2
    exit 23
fi
MOCK
cat >"$workdir/repo/packaging/appimage/build-appimage.sh" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf 'appimage %s %s\n' "$APPIMAGE_EXTRACT_AND_RUN" "$VERSION" >> "$PORTABLE_TEST_CALLS"
MOCK
chmod +x "$workdir/bin/flatpak" "$workdir/bin/flatpak-builder" \
    "$workdir/repo/packaging/appimage/build-appimage.sh" \
    "$workdir/repo/scripts/ci-linux-portable-recipes.sh"

export PATH="$workdir/bin:$PATH"
export PORTABLE_TEST_CALLS="$workdir/calls"

failure_log="$workdir/failure.log"
set +e
PORTABLE_TEST_FLATPAK_FAIL=1 \
    "$workdir/repo/scripts/ci-linux-portable-recipes.sh" flatpak \
    >"$failure_log" 2>&1
status=$?
set -e
if [[ $status -ne 23 ]]; then
    cat "$failure_log" >&2
    echo "expected Flatpak recipe failure status 23, got $status" >&2
    exit 1
fi
if grep -q 'SKIP' "$failure_log"; then
    cat "$failure_log" >&2
    echo "strict portable recipe script converted a failure to SKIP" >&2
    exit 1
fi

: >"$PORTABLE_TEST_CALLS"
"$workdir/repo/scripts/ci-linux-portable-recipes.sh" all >/dev/null
grep -q '^flatpak-builder ' "$PORTABLE_TEST_CALLS"
grep -q '^appimage 1 ci$' "$PORTABLE_TEST_CALLS"

printf 'strict portable recipe script contract: PASS\n'
