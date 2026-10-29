%global debug_package %{nil}

Name:           bare-eye
Version:        1.0.0
Release:        1%{?dist}
Summary:        Terminal dashboard for monitoring Linux servers over SSH

License:        MIT
URL:            https://github.com/fiandev/bare-eye
Source0:        bare-eye-%{version}.tar.gz

BuildRequires:  golang >= 1.26
BuildRequires:  go-rpm-macros

%description
Bare Eye is a local-first terminal dashboard for monitoring Linux VPS and
bare-metal servers over SSH. It collects CPU, memory, swap, load, disk,
network and uptime data with fixed read-only commands and renders everything
in one TUI. Nothing is installed on the monitored hosts.

%prep
%setup -q

%build
%gobuild -o bin/bare-eye ./cmd/bare-eye

%install
install -Dpm 0755 bin/bare-eye %{buildroot}%{_bindir}/bare-eye

%files
%license LICENSE
%doc README.md
%{_bindir}/bare-eye

%changelog
* Thu Oct 29 2026 Alfiansa <alfiansa@users.noreply.github.com> - 1.0.0-1
- Initial package
