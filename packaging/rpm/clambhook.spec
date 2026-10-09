# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# ClambHook RPM package for Fedora, the only supported RPM-based distribution.
#
# Build from the repository root, e.g.:
#   VERSION=$(git describe --tags --always | sed 's/^v//;s/-/./g')
#   tar --transform "s,^,clambhook-${VERSION}/," -czf ~/rpmbuild/SOURCES/clambhook-${VERSION}.tar.gz .
#   rpmbuild -bb packaging/rpm/clambhook.spec --define "version ${VERSION}"
#
# The Kotlin/Compose desktop controller is built with the Gradle wrapper and
# bundles a private jlink runtime under %%{_prefix}/lib/clambhook/ui. Release
# packages are signed with the developer@jpfchang.org key after the build
# (scripts/sign-linux-release-artifacts.sh).

%global debug_package %{nil}
%global _build_id_links none
# Dependency-license filenames are a frozen package contract. Fedora's default
# brp-compress pass would rename them with a .gz suffix.
%global __brp_compress %{nil}
# The bundled desktop runtime is private: never export its libraries as
# package-wide Provides, and never require them from the system.
%global __provides_exclude_from ^%{_prefix}/lib/clambhook/ui/.*$
%global __requires_exclude_from ^%{_prefix}/lib/clambhook/ui/.*$

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
BuildRequires:  java-21-openjdk-devel
BuildRequires:  java-21-openjdk-jmods
BuildRequires:  curl
BuildRequires:  libcurl-devel
BuildRequires:  libuv-devel
BuildRequires:  libsodium-devel
BuildRequires:  openssl-devel
BuildRequires:  systemd-rpm-macros

# libsecret is used via the secret-tool CLI for API token and license key
# storage against the host Secret Service.
Requires:       libsecret
Requires:       libsodium
Requires:       polkit
Requires:       systemd
Requires:       iproute
# Graphical session libraries used by the bundled desktop runtime.
Requires:       libX11
Requires:       libXext
Requires:       libXi
Requires:       libXrender
Requires:       libXtst
Requires:       fontconfig
Requires:       freetype
# The daemon runs as a dedicated unprivileged system user created in %%pre.
Requires(pre):  shadow-utils

%description
ClambHook is a private VPN and proxy router with its own protocol core and
local, metadata-first traffic inspection. This package installs the clambhook
daemon, the self-contained Kotlin/Compose desktop controller, the terminal
dashboard, and the license helper used for trial and license activation
against the hosted store backend.

New installations include a 7-day trial. Continued use requires a USD 79.99/year
subscription purchased from
store.swiphtgroup.com (Creem or NOWPayments; PayPal is not accepted).

%prep
%autosetup -n %{name}-%{version}

%build
export JAVA_HOME=%{_jvmdir}/java-21-openjdk
export PATH="$JAVA_HOME/bin:$PATH"
make build VERSION=%{version}
make build-linux VERSION=%{version}

%install
export JAVA_HOME=%{_jvmdir}/java-21-openjdk
export PATH="$JAVA_HOME/bin:$PATH"
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
install -Dpm 0644 packaging/repos/clambhook.repo %{buildroot}%{_sysconfdir}/yum.repos.d/clambhook.repo
install -Dpm 0644 keys/clambhook-release-key.asc %{buildroot}%{_sysconfdir}/pki/rpm-gpg/RPM-GPG-KEY-clambhook

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
%{_prefix}/lib/clambhook
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
# Signed dnf repository and the developer@jpfchang.org release key that
# verifies its packages (gpgcheck) and metadata (repo_gpgcheck).
%config(noreplace) %{_sysconfdir}/yum.repos.d/clambhook.repo
%{_sysconfdir}/pki/rpm-gpg/RPM-GPG-KEY-clambhook

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
