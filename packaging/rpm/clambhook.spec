# ClambHook RPM package for Fedora and Rocky Linux (RHEL-compatible).
#
# Build from the repository root, e.g.:
#   VERSION=$(git describe --tags --always | sed 's/^v//;s/-/./g')
#   tar --transform "s,^,clambhook-${VERSION}/," -czf ~/rpmbuild/SOURCES/clambhook-${VERSION}.tar.gz .
#   rpmbuild -bb packaging/rpm/clambhook.spec --define "version ${VERSION}"
#
# The Go build uses the in-tree vendor/ directory, so no network access is
# required during the build.

%global debug_package %{nil}
%global _build_id_links none

Name:           clambhook
Version:        %{?version}%{!?version:0.1.0}
Release:        1%{?dist}
Summary:        Private VPN and proxy router with local metadata-first inspection

# First-party materials are proprietary/source-available; vendored deps keep
# their own licenses. RPM's License field is advisory here.
License:        LicenseRef-Clambhook-Proprietary-View-Only
URL:            https://store.clambercloud.com/clambhook/
Source0:        %{name}-%{version}.tar.gz

BuildRequires:  golang >= 1.25
BuildRequires:  gcc
BuildRequires:  pkgconf-pkg-config
BuildRequires:  meson >= 1.0.0
BuildRequires:  ninja-build
BuildRequires:  vala
BuildRequires:  gtk4-devel
BuildRequires:  libadwaita-devel
BuildRequires:  libgee-devel
BuildRequires:  json-glib-devel
BuildRequires:  libsecret-devel
BuildRequires:  libsoup3-devel
BuildRequires:  libsodium-devel
BuildRequires:  glib2-devel

Requires:       gtk4
Requires:       libadwaita
Requires:       libgee
Requires:       json-glib
Requires:       libsecret
Requires:       libsoup3
Requires:       libsodium

%description
ClambHook is a private VPN and proxy router with its own protocol core and
local, metadata-first traffic inspection. This package installs the clambhook
daemon, the GTK/libadwaita desktop controller, the terminal dashboard, and the
private license helper used for trial and license activation against the hosted
store backend.

Continued use after the one-month trial requires a license purchased from
store.swiphtgroup.com (Creem or NOWPayments; PayPal is not accepted).

%prep
%autosetup -n %{name}-%{version}

%build
export CGO_ENABLED=1
make build VERSION=%{version}
make build-linux VERSION=%{version}

%install
make install DESTDIR=%{buildroot} PREFIX=%{_prefix}
make install-linux DESTDIR=%{buildroot} PREFIX=%{_prefix}

%files
%{_bindir}/clambhook
%{_bindir}/clambhook-tui
%{_bindir}/clambhook-license
%{_libexecdir}/clambhook
%{_libexecdir}/clambhook-license
%{_datadir}/applications/com.clambhook.Clambhook.desktop
%{_datadir}/metainfo/com.clambhook.Clambhook.metainfo.xml
%{_datadir}/icons/hicolor/1024x1024/apps/com.clambhook.Clambhook.png

%changelog
* Wed Jul 15 2026 Pengfan Chang <developer@jpfchang.org> - 0.1.0-1
- Initial ClambHook RPM for Fedora and Rocky Linux with daemon, GTK desktop
  controller, terminal dashboard, and license helper.
