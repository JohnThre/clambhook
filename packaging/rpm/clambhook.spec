Name:           clambhook
Version:        0.1.0
Release:        1%{?dist}
Summary:        Local network client daemon, desktop controller, and terminal dashboard

License:        GPL-3.0-only
URL:            https://github.com/JohnThre/clambhook
Source0:        %{url}/archive/refs/tags/v%{version}/%{name}-%{version}.tar.gz

BuildRequires:  gcc
BuildRequires:  golang
BuildRequires:  meson
BuildRequires:  ninja-build
BuildRequires:  make
BuildRequires:  pkgconfig
BuildRequires:  vala
BuildRequires:  pkgconfig(gee-0.8)
BuildRequires:  pkgconfig(gio-2.0)
BuildRequires:  pkgconfig(glib-2.0)
BuildRequires:  pkgconfig(gtk4)
BuildRequires:  pkgconfig(json-glib-1.0)
BuildRequires:  pkgconfig(libadwaita-1)
BuildRequires:  pkgconfig(libsecret-1)
BuildRequires:  pkgconfig(libsodium)
BuildRequires:  pkgconfig(libsoup-3.0)

%description
clambhook provides a local network client daemon, HTTP control API, native GTK
desktop controller, and terminal dashboard.

%prep
%autosetup -n %{name}-%{version}

%build
%make_build build VERSION=%{version}
%make_build build-linux VERSION=%{version}

%install
%make_install PREFIX=%{_prefix}
make install-linux DESTDIR=%{buildroot} PREFIX=%{_prefix}

%check
%make_build test

%files
%license LICENSE
%doc README.md configs/example.toml
%{_bindir}/clambhook
%{_bindir}/clambhook-tui
%{_bindir}/clambhook-linux
%{_libexecdir}/clambhook
%{_datadir}/applications/com.clambhook.Clambhook.desktop
%{_datadir}/metainfo/com.clambhook.Clambhook.metainfo.xml
%{_datadir}/icons/hicolor/1024x1024/apps/com.clambhook.Clambhook.png

%changelog
* Fri May 22 2026 clambhook contributors <noreply@example.invalid> - 0.1.0-1
- Initial RPM packaging.
