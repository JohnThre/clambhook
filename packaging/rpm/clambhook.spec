# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# ClambHook RPM package for the Fedora validation and release lane.
#
# Build from the repository root, e.g.:
#   VERSION=$(git describe --tags --always | sed 's/^v//;s/-/./g')
#   tar --transform "s,^,clambhook-${VERSION}/," -czf ~/rpmbuild/SOURCES/clambhook-${VERSION}.tar.gz .
#   rpmbuild -bb packaging/rpm/clambhook.spec --define "version ${VERSION}"
#
# CI supplies the checksum-pinned GraalVM 17 toolchain. Maven, Gluon, and the
# Linux AArch64 preparation script verify every explicitly pinned build input.

%global debug_package %{nil}
%global _build_id_links none

Name:           clambhook
Version:        %{?version}%{!?version:1.0.2}
Release:        1%{?dist}
Summary:        Private VPN and proxy router with local metadata-first inspection

# The distributed application is GPL-3.0-only. Its reusable crypto libraries
# are Apache-2.0 and vendored dependencies retain their upstream licenses.
License:        GPL-3.0-only AND Apache-2.0
URL:            https://store.clambercloud.com/clambhook/
Source0:        %{name}-%{version}.tar.gz

BuildRequires:  gcc
BuildRequires:  cmake
BuildRequires:  ninja-build
BuildRequires:  pkgconf-pkg-config
BuildRequires:  maven
BuildRequires:  curl
BuildRequires:  python3
BuildRequires:  alsa-lib-devel
BuildRequires:  pkgconfig(libavcodec)
BuildRequires:  pkgconfig(libavformat)
BuildRequires:  pkgconfig(libavutil)
BuildRequires:  freetype-devel
BuildRequires:  gtk3-devel
BuildRequires:  libX11-devel
BuildRequires:  libXtst-devel
BuildRequires:  libcurl-devel
BuildRequires:  pkgconfig(libdrm)
BuildRequires:  libuv-devel
BuildRequires:  libsodium-devel
BuildRequires:  pkgconfig(egl)
BuildRequires:  pkgconfig(gbm)
BuildRequires:  mesa-libGL-devel
BuildRequires:  openssl-devel
BuildRequires:  pango-devel
BuildRequires:  systemd-rpm-macros
BuildRequires:  zlib-devel

# libsecret is used via the secret-tool CLI for API token and license key
# storage against the host Secret Service.
Requires:       libsecret
Requires:       libsodium
Requires:       polkit
Requires:       systemd
Requires:       iproute
# The daemon runs as a dedicated unprivileged system user created in %%pre.
Requires(pre):  shadow-utils

%description
ClambHook is a private VPN and proxy router with its own protocol core and
local, metadata-first traffic inspection. This package installs the clambhook
daemon, the self-contained JavaFX/Gluon desktop controller, the terminal
dashboard, and the license helper used for trial and license activation
against the hosted store backend.

Continued use after the one-month trial requires a license purchased from
store.swiphtgroup.com (Creem or NOWPayments; PayPal is not accepted).

%prep
%autosetup -n %{name}-%{version}

%build
test -n "$GRAALVM_HOME"
make build VERSION=%{version}
make build-linux VERSION=%{version}

%install
make install-linux DESTDIR=%{buildroot} PREFIX=%{_prefix}
# %%license installs the two first-party licenses in the RPM license directory.
# Drop the generic CMake documentation copies to avoid duplicate, unpackaged
# payload under %%{_datadir}/doc.
rm -f %{buildroot}%{_datadir}/doc/clambhook/LICENSE
rm -f %{buildroot}%{_datadir}/doc/clambhook/LICENSE-APACHE
install -Dpm 0644 packaging/config/config.toml %{buildroot}%{_sysconfdir}/clambhook/config.toml
install -Dpm 0644 packaging/systemd/clambhook-sysusers.conf %{buildroot}%{_sysusersdir}/clambhook.conf
install -Dpm 0644 packaging/systemd/clambhook-tmpfiles.conf %{buildroot}%{_tmpfilesdir}/clambhook.conf
install -d %{buildroot}%{_localstatedir}/lib/clambhook

%pre
# Create the dedicated system user/group before the payload is laid down so the
# %%attr ownership below (and the daemon's least-privilege runtime user) resolve.
getent group clambhook >/dev/null || groupadd -r clambhook
getent passwd clambhook >/dev/null || \
    useradd -r -g clambhook -d %{_localstatedir}/lib/clambhook -s /sbin/nologin \
            -c "ClambHook daemon" clambhook
exit 0

%post
# Reconcile the runtime user and create/own the config + state directories,
# then register the service.
%sysusers_create_compat %{_sysusersdir}/clambhook.conf
%tmpfiles_create %{_tmpfilesdir}/clambhook.conf
%systemd_post clambhook-daemon.service

%preun
%systemd_preun clambhook-daemon.service

%postun
%systemd_postun_with_restart clambhook-daemon.service

%files
%license LICENSE LICENSE-APACHE
# These documentation files are already installed by CMake.  Use absolute
# buildroot paths so rpm marks them as documentation without recreating (and
# replacing) /usr/share/doc/clambhook after the nested dependency licenses
# have been installed there.
%doc %{_datadir}/doc/clambhook/LICENSING.md
%doc %{_datadir}/doc/clambhook/NOTICE
%doc %{_datadir}/doc/clambhook/TRADEMARKS.md
%doc %{_datadir}/doc/clambhook/THIRD_PARTY_NOTICES.md
%{_bindir}/clambhook
%{_bindir}/clambhook-tui
%{_bindir}/clambhook-license
%{_bindir}/clambhook-ui
%{_datadir}/doc/clambhook/licenses
%{_datadir}/applications/org.jpfchang.clambhook.desktop
%{_datadir}/metainfo/org.jpfchang.clambhook.metainfo.xml
%{_datadir}/icons/hicolor/1024x1024/apps/org.jpfchang.clambhook.png
# The daemon's runtime user owns its config directory so it can atomically
# rewrite config, rule-set/subscription caches, and the developer CA. The
# config file itself stays root-owned but group-readable by the daemon.
%attr(0750,clambhook,clambhook) %dir %{_sysconfdir}/clambhook
%attr(0640,root,clambhook) %config(noreplace) %{_sysconfdir}/clambhook/config.toml
# Owned so rpm tracks it; the daemon's StateDirectory=clambhook keeps it correct
# at runtime. %attr sets ownership to the runtime user up front (created in %%pre).
%attr(0750,clambhook,clambhook) %dir %{_localstatedir}/lib/clambhook
%{_sysusersdir}/clambhook.conf
%{_tmpfilesdir}/clambhook.conf
%{_unitdir}/clambhook-daemon.service
%{_datadir}/polkit-1/actions/com.clambhook.Clambhook.policy

%changelog
* Sat Aug 29 2026 Pengfan Chang <support@swiphtgroup.com> - 1.0.2-1
- Complete C17 daemon and self-contained JavaFX/Gluon native-image cutover.

* Wed Jul 22 2026 Pengfan Chang <developer@jpfchang.org> - 1.0.1-1
- Release 1.0.1 maintenance update.

* Mon Jul 20 2026 Pengfan Chang <developer@jpfchang.org> - 0.1.0-2
- Run clambhook-daemon.service as a dedicated unprivileged clambhook user with
  only CAP_NET_ADMIN/CAP_NET_RAW; create the user via shadow-utils/sysusers and
  own the config/state directories via tmpfiles and %%attr.
* Wed Jul 15 2026 Pengfan Chang <developer@jpfchang.org> - 0.1.0-1
- Initial ClambHook RPM with daemon, desktop controller, terminal dashboard,
  and license helper.
