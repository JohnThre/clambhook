#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Build, checksum, and GPG-sign the ClambHook GNU/Linux GitHub Release assets.
#
#   UPDATE_CHANNEL=stable REQUIRE_SIGNING=1 GPG_KEY=EAA876B70B1832F5 \
#     scripts/release-linux.sh
#
# Produces per-package .sha256 and detached .sha256.sig files (armored GPG),
# matching the macOS release convention.
set -euo pipefail

echo "Building GNU/Linux release assets for GitHub Releases." >&2

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

"$ROOT_DIR/scripts/check-source-only.sh" "$ROOT_DIR"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)}"
UPDATE_CHANNEL="${UPDATE_CHANNEL:-stable}"
REQUIRE_SIGNING="${REQUIRE_SIGNING:-1}"
GPG_KEY="${GPG_KEY:-EAA876B70B1832F5}"
case "$(uname -m)" in
  x86_64|amd64) ARCH="x86_64" ;;
  aarch64|arm64) ARCH="aarch64" ;;
  *) echo "Unsupported GNU/Linux release architecture: $(uname -m)" >&2; exit 2 ;;
esac
DIST_DIR="$ROOT_DIR/dist/linux"
RELEASE_TAG="${RELEASE_TAG:-v${VERSION}}"
RELEASE_BASE="https://github.com/${GITHUB_REPOSITORY:-JohnThre/clambhook}/releases/download/${RELEASE_TAG}"

if [[ "${CLAMBHOOK_RELEASE_APPEND:-0}" != "1" ]]; then
  rm -rf "$DIST_DIR"
fi
mkdir -p "$DIST_DIR"

require() { command -v "$1" >/dev/null 2>&1 || {
  echo "$1 is required for $2." >&2
  exit 2
}; }

gpg_sign() {
  local target="$1"
  local passphrase_args=()
  if [[ "$REQUIRE_SIGNING" != "1" ]]; then
    echo "REQUIRE_SIGNING!=1: skipping signature for $target" >&2
    return 0
  fi
  require gpg "release signing"
  if [[ -n "${GPG_PASSPHRASE_FILE:-}" ]]; then
    [[ -f "$GPG_PASSPHRASE_FILE" ]] || {
      echo "GPG_PASSPHRASE_FILE does not exist." >&2
      exit 2
    }
    passphrase_args=(--passphrase-file "$GPG_PASSPHRASE_FILE")
  fi
  gpg --batch --yes --pinentry-mode loopback --local-user "$GPG_KEY" \
    "${passphrase_args[@]}" \
    --detach-sign --armor --output "$target.sig" "$target"
  echo "GPG-signed $target → $target.sig"
}

checksum_and_sign() {
  # checksum_and_sign <artifact-path>
  local artifact="$1"
  local name
  name="$(basename "$artifact")"
  (cd "$(dirname "$artifact")" && sha256sum "$name" >"$name.sha256")
  gpg_sign "$artifact.sha256"
  echo "  sha256: $(awk '{print $1}' "$artifact.sha256")"
}

# 1. Ubuntu / Debian-format package (.deb)
build_deb() {
  require dpkg-buildpackage ".deb build"
  dpkg-buildpackage -us -uc -b
  local built
  # shellcheck disable=SC2012 # Package filenames are controlled by dpkg.
  built="$(ls -t ../clambhook_*_*.deb | head -n1)"
  cp "$built" "$DIST_DIR/ClambHook-${VERSION}-${ARCH}.deb"
  checksum_and_sign "$DIST_DIR/ClambHook-${VERSION}-${ARCH}.deb"
}

# 2. Fedora Linux RPM package (.rpm)
build_rpm() {
  require rpmbuild ".rpm build"
  local topdir="$DIST_DIR/rpmbuild"
  mkdir -p "$topdir"/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
  local rpmver="${VERSION//-/.}"
  tar --exclude-vcs --exclude='./dist' --exclude='./build-native*' \
    --exclude='./build-gluon-linux-aarch64' \
    --exclude='./ui/javafx/target' --exclude='./ui/android/.gradle' \
    --exclude='./ui/android/.native-deps' --exclude='./ui/android/app/build' \
    --transform "s,^\.,clambhook-${rpmver}," \
    -czf "$topdir/SOURCES/clambhook-${rpmver}.tar.gz" .
  rpmbuild --define "_topdir $topdir" --define "version ${rpmver}" \
    -bb packaging/rpm/clambhook.spec
  local built
  # shellcheck disable=SC2012 # Package filenames are controlled by rpmbuild.
  built="$(ls -t "$topdir"/RPMS/*/clambhook-*.rpm | head -n1)"
  cp "$built" "$DIST_DIR/ClambHook-${VERSION}-${ARCH}.rpm"
  checksum_and_sign "$DIST_DIR/ClambHook-${VERSION}-${ARCH}.rpm"
}

TARGETS="${1:-deb rpm}"
for target in $TARGETS; do
  echo "== Building $target =="
  "build_$target"
done

# Generate the GNU/Linux update manifest after all packages are built and signed.
MANIFEST="$DIST_DIR/clambhook-linux-${ARCH}-manifest.json"
PUBLISHED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

write_manifest_entry() {
  local pkg="$1" file_suffix="$2"
  local artifact="$DIST_DIR/ClambHook-${VERSION}-${ARCH}.${file_suffix}"
  local sha256=""
  if [[ -f "$artifact.sha256" ]]; then
    sha256="$(awk '{print $1}' "$artifact.sha256")"
  fi
  printf '    "%s": {\n' "$pkg"
  printf '      "url": "%s/%s",\n' "$RELEASE_BASE" "$(basename "$artifact")"
  if [[ -n "$sha256" ]]; then
    printf '      "sha256": "%s"\n' "$sha256"
  else
    printf '      "sha256": ""\n'
  fi
  printf '    }'
}

{
  printf '{\n'
  printf '  "version": "%s",\n' "$VERSION"
  printf '  "publishedAt": "%s",\n' "$PUBLISHED_AT"
  printf '  "architecture": "%s",\n' "$ARCH"
  printf '  "packages": {\n'
  write_manifest_entry "deb" "deb"
  printf ',\n'
  write_manifest_entry "rpm" "rpm"
  printf '\n  }\n'
  printf '}\n'
} >"$MANIFEST"

gpg_sign "$MANIFEST"

echo "Generated $MANIFEST"

cat <<SUMMARY

Linux release artifacts written to $DIST_DIR
Publish these files on GitHub Release $RELEASE_TAG.
SUMMARY
