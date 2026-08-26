#!/usr/bin/env bash
# Build, checksum, and GPG-sign the ClambHook GNU/Linux release artifacts, then
# print the Cloudflare R2 keys and clambercloud.com env vars to publish. Run on
# a GNU/Linux build host from the repository root:
#
#   UPDATE_CHANNEL=stable REQUIRE_SIGNING=1 GPG_KEY=EAA876B70B1832F5 \
#     scripts/release-linux.sh
#
# Produces per-package .sha256 and detached .sha256.sig files (armored GPG),
# matching the macOS release convention. Never publish these artifacts on GitHub
# Releases or package mirrors — upload only to the store.clambercloud.com R2
# bucket and set the CLAMBHOOK_*_LINUX_* URL variables on the Pages project.
set -euo pipefail

echo "internal-only: building GNU/Linux release artifacts for store.clambercloud.com." >&2

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

"$ROOT_DIR/scripts/check-source-only.sh" "$ROOT_DIR"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)}"
UPDATE_CHANNEL="${UPDATE_CHANNEL:-stable}"
CHAN="$(echo "$UPDATE_CHANNEL" | tr '[:lower:]' '[:upper:]')"
REQUIRE_SIGNING="${REQUIRE_SIGNING:-1}"
GPG_KEY="${GPG_KEY:-EAA876B70B1832F5}"
ARCH="$(uname -m)"
DIST_DIR="$ROOT_DIR/dist/linux"
BUCKET="${CLAMBHOOK_R2_BUCKET:-clambhook-artifacts}"

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

# 1. Trisquel / Debian-format package (.deb)
build_deb() {
  require dpkg-buildpackage ".deb build"
  dpkg-buildpackage -us -uc -b
  local built
  # shellcheck disable=SC2012 # Package filenames are controlled by dpkg.
  built="$(ls -t ../clambhook_*_*.deb | head -n1)"
  cp "$built" "$DIST_DIR/clambhook-${VERSION}-${ARCH}.deb"
  checksum_and_sign "$DIST_DIR/clambhook-${VERSION}-${ARCH}.deb"
}

# 2. Rocky Linux / AlmaLinux RPM package (.rpm)
build_rpm() {
  require rpmbuild ".rpm build"
  local topdir="$DIST_DIR/rpmbuild"
  mkdir -p "$topdir"/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
  local rpmver="${VERSION//-/.}"
  tar --exclude-vcs --exclude='./dist' \
    --transform "s,^\.,clambhook-${rpmver}," \
    -czf "$topdir/SOURCES/clambhook-${rpmver}.tar.gz" .
  rpmbuild --define "_topdir $topdir" --define "version ${rpmver}" \
    -bb packaging/rpm/clambhook.spec
  local built
  # shellcheck disable=SC2012 # Package filenames are controlled by rpmbuild.
  built="$(ls -t "$topdir"/RPMS/*/clambhook-*.rpm | head -n1)"
  cp "$built" "$DIST_DIR/clambhook-${VERSION}-${ARCH}.rpm"
  checksum_and_sign "$DIST_DIR/clambhook-${VERSION}-${ARCH}.rpm"
}

TARGETS="${1:-deb rpm}"
for target in $TARGETS; do
  echo "== Building $target =="
  "build_$target"
done

# Generate the GNU/Linux update manifest after all packages are built and signed.
# The website serves this at /api/clambhook/linux-manifest so users and package
# managers can discover the current release without hitting GitHub.
MANIFEST="$DIST_DIR/clambhook-linux-manifest.json"
PUBLISHED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

write_manifest_entry() {
  local pkg="$1" file_suffix="$2" url_key="$3"
  local artifact="$DIST_DIR/clambhook-${VERSION}-${ARCH}.${file_suffix}"
  local sha256=""
  if [[ -f "$artifact.sha256" ]]; then
    sha256="$(awk '{print $1}' "$artifact.sha256")"
  fi
  printf '    "%s": {\n' "$pkg"
  # shellcheck disable=SC2016 # Emit a literal website environment placeholder.
  printf '      "url": "${%s}",\n' "$url_key"
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
  write_manifest_entry "deb" "deb" "CLAMBHOOK_${CHAN}_LINUX_DEB_URL"
  printf ',\n'
  write_manifest_entry "rpm" "rpm" "CLAMBHOOK_${CHAN}_LINUX_RPM_URL"
  printf '\n  }\n'
  printf '}\n'
} >"$MANIFEST"

gpg_sign "$MANIFEST"

echo "Generated $MANIFEST"

cat <<SUMMARY

Linux release artifacts written to $DIST_DIR
Upload each to r2://$BUCKET/clambhook/linux/ and set these Pages variables:
  CLAMBHOOK_${CHAN}_LINUX_DEB_URL          → clambhook-${VERSION}-${ARCH}.deb
  CLAMBHOOK_${CHAN}_LINUX_DEB_SHA256_URL   → clambhook-${VERSION}-${ARCH}.deb.sha256
  CLAMBHOOK_${CHAN}_LINUX_RPM_URL          → clambhook-${VERSION}-${ARCH}.rpm
  CLAMBHOOK_${CHAN}_LINUX_RPM_SHA256_URL   → clambhook-${VERSION}-${ARCH}.rpm.sha256
  CLAMBHOOK_${CHAN}_LINUX_MANIFEST_URL     → clambhook-linux-manifest.json
Do not publish these on GitHub Releases or package mirrors.
SUMMARY
