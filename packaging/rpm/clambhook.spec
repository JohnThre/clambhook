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
Version:        %{?version}%{!?version:1.0.1}
Release:        1%{?dist}
Summary:        Private VPN and proxy router with local metadata-first inspection

# First-party materials are proprietary/source-available; vendored deps keep
# their own licenses. RPM's License field is advisory here.
License:        LicenseRef-Clambhook-Proprietary-View-Only
URL:            https://store.clambercloud.com/clambhook/
Source0:        %{name}-%{version}.tar.gz

BuildRequires:  gcc
# BuildRequires: golang  # deliberately omitted: the spec uses /usr/local/go
# installed by the release harness so the exact go.mod Go version is used.
BuildRequires:  pkgconf-pkg-config
# The Linux desktop controller is built with Kotlin/Compose Multiplatform on
# the JVM; the daemon is built with Go. JDK 17+ provides javac/gradle.
BuildRequires:  java-17-openjdk-devel
BuildRequires:  libsodium-devel
BuildRequires:  glib2-devel
BuildRequires:  systemd-rpm-macros

# The desktop controller bundles its own JVM runtime via the Gradle
# installDist distribution, so it only needs a JRE at runtime.
Requires:       java-17-openjdk-headless
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
daemon, the Kotlin/Compose Multiplatform desktop controller, the terminal
dashboard, and the private license helper used for trial and license activation
against the hosted store backend.

Continued use after the one-month trial requires a license purchased from
store.swiphtgroup.com (Creem or NOWPayments; PayPal is not accepted).

%prep
%autosetup -n %{name}-%{version}

%build
export CGO_ENABLED=1
# Prefer a project-provided or builder-installed Go over the system one so
# the exact Go version from go.mod is used, while still satisfying the loose
# BuildRequires.
if [ -x /usr/local/go/bin/go ]; then export PATH=/usr/local/go/bin:$PATH; fi
export GOTOOLCHAIN=auto
make build VERSION=%{version}
make build-linux VERSION=%{version}

%install
if [ -x /usr/local/go/bin/go ]; then export PATH=/usr/local/go/bin:$PATH; fi
export GOTOOLCHAIN=auto
make install DESTDIR=%{buildroot} PREFIX=%{_prefix}
make install-linux DESTDIR=%{buildroot} PREFIX=%{_prefix}
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
%{_bindir}/clambhook
%{_bindir}/clambhook-tui
%{_bindir}/clambhook-license
%{_bindir}/clambhook-linux
%{_libdir}/clambhook-linux
%{_libexecdir}/clambhook
%{_libexecdir}/clambhook-license
%{_datadir}/applications/com.clambhook.Clambhook.desktop
%{_datadir}/metainfo/com.clambhook.Clambhook.metainfo.xml
%{_datadir}/icons/hicolor/1024x1024/apps/com.clambhook.Clambhook.png
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
* Wed Jul 22 2026 Pengfan Chang <developer@jpfchang.org> - 1.0.1-1
- Release 1.0.1: Kotlin/Compose Multiplatform desktop controller and Go daemon.

* Mon Jul 20 2026 Pengfan Chang <developer@jpfchang.org> - 0.1.0-2
- Run clambhook-daemon.service as a dedicated unprivileged clambhook user with
  only CAP_NET_ADMIN/CAP_NET_RAW; create the user via shadow-utils/sysusers and
  own the config/state directories via tmpfiles and %%attr.
* Wed Jul 15 2026 Pengfan Chang <developer@jpfchang.org> - 0.1.0-1
- Initial ClambHook RPM for Fedora and Rocky Linux with daemon, Kotlin/Compose
  Multiplatform desktop controller, terminal dashboard, and license helper.