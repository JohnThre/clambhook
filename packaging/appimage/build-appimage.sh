#!/usr/bin/env bash
# Build a universal ClambHook AppImage that runs on Ubuntu, Debian, PureOS,
# Fedora, and Rocky without system JVM development packages.
#
# Run on a GNU/Linux build host (x86_64 or aarch64) from the repository root:
#   packaging/appimage/build-appimage.sh
#
# Requires: the JDK 17+/Go build toolchain (same as the .deb build
# dependencies), plus curl and file. linuxdeploy and appimagetool are
# downloaded on first run into packaging/appimage/tools/. Compose Multiplatform
# bundles its own JVM runtime in the Gradle installDist distribution, so no
# system GTK or JRE bundling plugin is needed.
#
# The AppImage runs the daemon in System Proxy mode. Device-wide Enhanced/TUN
# routing needs CAP_NET_ADMIN and is only available from the native .deb/.rpm
# packages.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

arch="$(uname -m)"
case "$arch" in
  x86_64) ld_arch="x86_64" ;;
  aarch64|arm64) ld_arch="aarch64" ;;
  *) echo "Unsupported architecture for AppImage: $arch" >&2; exit 2 ;;
esac

version="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
appdir="$repo_root/packaging/appimage/AppDir"
tools="$repo_root/packaging/appimage/tools"
out="$repo_root/dist/Clambhook-${version}-${arch}.AppImage"

rm -rf "$appdir"
mkdir -p "$appdir" "$tools" "$repo_root/dist"

# 1. Build and stage the app under AppDir/usr. The Linux desktop controller is
# built with Kotlin/Compose Multiplatform; `make build-linux` drives Gradle
# (./gradlew installDist under ui/linux) and produces a self-contained JVM
# distribution that bundles its own runtime, so no external JRE is required at
# runtime.
make build VERSION="$version"
make install-linux DESTDIR="$appdir" PREFIX=/usr

# Generate a 512x512 icon (linuxdeploy rejects 1024x1024).
icon_src="$repo_root/clambhook-icon-1024.png"
icon_512="$appdir/usr/share/icons/hicolor/512x512/apps/com.clambhook.Clambhook.png"
mkdir -p "$(dirname "$icon_512")"
if command -v convert >/dev/null 2>&1; then
  convert "$icon_src" -resize 512x512 "$icon_512"
elif command -v sips >/dev/null 2>&1; then
  sips -z 512 512 "$icon_src" --out "$icon_512" >/dev/null 2>&1
else
  cp "$icon_src" "$icon_512"
fi

# 2. Fetch bundling tools (idempotent), pinned to specific upstream versions
# and verified against recorded SHA-256 digests before they are made
# executable or run. linuxdeploy and appimagetool are pinned to tagged
# releases. Refresh a pin by updating both the URL and its digest here
# together.
ld_release="1-alpha-20251107-1"
appimagetool_release="1.9.0"

declare -A sha256=(
  ["linuxdeploy-x86_64.AppImage"]="c20cd71e3a4e3b80c3483cef793cda3f4e990aca14014d23c544ca3ce1270b4d"
  ["linuxdeploy-aarch64.AppImage"]="620095110d693282b8ebeb244a95b5e911cf8f65f76c88b4b47d16ae6346fcff"
  ["appimagetool-x86_64.AppImage"]="46fdd785094c7f6e545b61afcfb0f3d98d8eab243f644b4b17698c01d06083d1"
  ["appimagetool-aarch64.AppImage"]="04f45ea45b5aa07bb2b071aed9dbf7a5185d3953b11b47358c1311f11ea94a96"
)

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

# fetch downloads $url to $dest and verifies it against the digest recorded for
# $key BEFORE making it executable, so an unpinned or tampered artifact is
# never chmod'd or run. A cached file that already matches is reused; any
# mismatch is fatal.
fetch() {
  local url="$1" dest="$2" key="$3"
  local want="${sha256[$key]:-}"
  if [ -z "$want" ]; then
    echo "No recorded SHA-256 for $key" >&2
    exit 3
  fi
  if [ -f "$dest" ] && [ "$(sha256_of "$dest")" = "$want" ]; then
    chmod +x "$dest"
    return 0
  fi
  echo "Downloading $(basename "$dest")"
  rm -f "$dest"
  curl -fsSL "$url" -o "$dest"
  local got
  got="$(sha256_of "$dest")"
  if [ "$got" != "$want" ]; then
    rm -f "$dest"
    echo "SHA-256 mismatch for $key:" >&2
    echo "  expected $want" >&2
    echo "  actual   $got" >&2
    exit 4
  fi
  chmod +x "$dest"
}
ld_base="https://github.com/linuxdeploy"
fetch "$ld_base/linuxdeploy/releases/download/${ld_release}/linuxdeploy-${ld_arch}.AppImage" \
  "$tools/linuxdeploy-${ld_arch}.AppImage" "linuxdeploy-${ld_arch}.AppImage"
fetch "https://github.com/AppImage/appimagetool/releases/download/${appimagetool_release}/appimagetool-${ld_arch}.AppImage" \
  "$tools/appimagetool-${ld_arch}.AppImage" "appimagetool-${ld_arch}.AppImage"

# 3. Pack the AppImage. The Compose Multiplatform JVM distribution installed
# under AppDir/usr bundles its own runtime, so linuxdeploy is only used to
# assemble the AppDir structure and produce the final AppImage (no GTK plugin
# or DEPLOY_GTK_VERSION is needed).
export OUTPUT="$out"
"$tools/linuxdeploy-${ld_arch}.AppImage" \
  --appdir "$appdir" \
  --desktop-file "$appdir/usr/share/applications/com.clambhook.Clambhook.desktop" \
  --icon-file "$appdir/usr/share/icons/hicolor/512x512/apps/com.clambhook.Clambhook.png" \
  --output appimage

echo "Built $out"