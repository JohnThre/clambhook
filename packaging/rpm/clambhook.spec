Name:           clambhook
Version:        0.1.0
Release:        1%{?dist}
Summary:        Local network client daemon and terminal dashboard

License:        GPL-3.0-only
URL:            https://github.com/JohnThre/clambhook
Source0:        %{url}/archive/refs/tags/v%{version}/%{name}-%{version}.tar.gz

BuildRequires:  gcc
BuildRequires:  golang
BuildRequires:  make
BuildRequires:  pkgconfig
BuildRequires:  pkgconfig(libsodium)

%description
clambhook provides a local network client daemon, HTTP control API, and terminal
dashboard.

%prep
%autosetup -n %{name}-%{version}

%build
%make_build build VERSION=%{version}

%install
%make_install PREFIX=%{_prefix}

%check
%make_build test

%files
%license LICENSE
%doc README.md configs/example.toml
%{_bindir}/clambhook
%{_bindir}/clambhook-tui

%changelog
* Fri May 22 2026 clambhook contributors <noreply@example.invalid> - 0.1.0-1
- Initial RPM packaging.
