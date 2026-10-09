#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Build, checksum, and GPG-sign the ClambHook GNU/Linux GitHub Release assets
# for the only supported distributions: Ubuntu (.deb) and Fedora (.rpm).
#
#   UPDATE_CHANNEL=stable REQUIRE_SIGNING=1 GPG_KEY=EAA876B70B1832F5 \
#     scripts/release-linux.sh            # build deb and rpm
#   scripts/release-linux.sh manifest     # only regenerate the manifest
#
# Containerized builds run with REQUIRE_SIGNING=0. The protected runner then
# embeds package signatures and detached .sig files with
# scripts/sign-linux-release-artifacts.sh, which regenerates checksums and the
# manifest afterwards because embedding a signature changes package bytes.
set -euo pipefail

echo "Building GNU/Linux release assets for GitHub Releases." >&2

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

"$ROOT_DIR/scripts/check-source-only.sh" "$ROOT_DIR"
# shellcheck source=scripts/lib/release-gpg.sh
source "$ROOT_DIR/scripts/lib/release-gpg.sh"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)}"
UPDATE_CHANNEL="${UPDATE_CHANNEL:-stable}"
case "$(uname -m)" in
  x86_64|amd64) ARCH="x86_64" ;;
  aarch64|arm64) ARCH="aarch64" ;;
  *) echo "Unsupported GNU/Linux release architecture: $(uname -m)" >&2; exit 2 ;;
esac
DIST_DIR="${CLAMBHOOK_LINUX_DIST_DIR:-$ROOT_DIR/dist/linux}"
RELEASE_TAG="${RELEASE_TAG:-v${VERSION}}"
RELEASE_BASE="https://github.com/${GITHUB_REPOSITORY:-JohnThre/clambhook}/releases/download/${RELEASE_TAG}"

TARGETS="${1:-deb rpm}"
if [[ "$TARGETS" == "manifest" ]]; then
  TARGETS=""
elif [[ "${CLAMBHOOK_RELEASE_APPEND:-0}" != "1" ]]; then
  rm -rf "$DIST_DIR"
fi
mkdir -p "$DIST_DIR"

require() { command -v "$1" >/dev/null 2>&1 || {
  echo "$1 is required for $2." >&2
  exit 2
}; }

# 1. Ubuntu / Debian-format package (.deb)
build_deb() {
  require dpkg-buildpackage ".deb build"
  dpkg-buildpackage -us -uc -b
  local built
  # shellcheck disable=SC2012 # Package filenames are controlled by dpkg.
  built="$(ls -t ../clambhook_*_*.deb | head -n1)"
  cp "$built" "$DIST_DIR/ClambHook-${VERSION}-${ARCH}.deb"
  ch_checksum_and_sign "$DIST_DIR/ClambHook-${VERSION}-${ARCH}.deb"
}

# 2. Fedora Linux RPM package (.rpm)
build_rpm() {
  require rpmbuild ".rpm build"
  local topdir="$DIST_DIR/rpmbuild"
  mkdir -p "$topdir"/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
  local rpmver="${VERSION//-/.}"
  tar --exclude-vcs --exclude='./dist' --exclude='./build-native*' \
    --exclude='./ui/kotlin/.gradle' --exclude='./ui/kotlin/.kotlin' \
    --exclude='./ui/kotlin/.native-deps' --exclude='./ui/kotlin/*/build' \
    --exclude='./ui/kotlin/build' --exclude='./ui/kotlin/platform/.cxx' \
    --transform "s,^\.,clambhook-${rpmver}," \
    -czf "$topdir/SOURCES/clambhook-${rpmver}.tar.gz" .
  rpmbuild --define "_topdir $topdir" --define "version ${rpmver}" \
    -bb packaging/rpm/clambhook.spec
  local built
  # shellcheck disable=SC2012 # Package filenames are controlled by rpmbuild.
  built="$(ls -t "$topdir"/RPMS/*/clambhook-*.rpm | head -n1)"
  cp "$built" "$DIST_DIR/ClambHook-${VERSION}-${ARCH}.rpm"
  ch_checksum_and_sign "$DIST_DIR/ClambHook-${VERSION}-${ARCH}.rpm"
}

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

ch_gpg_sign "$MANIFEST"

echo "Generated $MANIFEST"

cat <<SUMMARY

Linux release artifacts written to $DIST_DIR
Publish these files on GitHub Release $RELEASE_TAG.
SUMMARY
