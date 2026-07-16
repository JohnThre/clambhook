#!/usr/bin/env bash
# Build a universal ClambHook AppImage that runs on Ubuntu, Debian, PureOS,
# Fedora, and Rocky without system GTK development packages.
#
# Run on a GNU/Linux build host (x86_64 or aarch64) from the repository root:
#   packaging/appimage/build-appimage.sh
#
# Requires: the GTK4/libadwaita/Vala/Go build toolchain (same as the .deb build
# dependencies), plus curl and file. linuxdeploy, its GTK plugin, and
# appimagetool are downloaded on first run into packaging/appimage/tools/.
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

# 1. Build and stage the app under AppDir/usr.
make build VERSION="$version"
make install-linux DESTDIR="$appdir" PREFIX=/usr

# 2. Fetch bundling tools (idempotent).
fetch() {
  local url="$1" dest="$2"
  if [ ! -x "$dest" ]; then
    echo "Downloading $(basename "$dest")"
    curl -fsSL "$url" -o "$dest"
    chmod +x "$dest"
  fi
}
base="https://github.com/linuxdeploy"
fetch "$base/linuxdeploy/releases/download/continuous/linuxdeploy-${ld_arch}.AppImage" "$tools/linuxdeploy-${ld_arch}.AppImage"
fetch "$base/linuxdeploy-plugin-gtk/releases/download/continuous/linuxdeploy-plugin-gtk.sh" "$tools/linuxdeploy-plugin-gtk.sh"
fetch "https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-${ld_arch}.AppImage" "$tools/appimagetool-${ld_arch}.AppImage"

# 3. Bundle GTK4/libadwaita and their runtime deps, then pack the AppImage.
export OUTPUT="$out"
export DEPLOY_GTK_VERSION=4
"$tools/linuxdeploy-${ld_arch}.AppImage" \
  --appdir "$appdir" \
  --plugin gtk \
  --desktop-file "$appdir/usr/share/applications/com.clambhook.Clambhook.desktop" \
  --icon-file "$appdir/usr/share/icons/hicolor/1024x1024/apps/com.clambhook.Clambhook.png" \
  --output appimage

echo "Built $out"
