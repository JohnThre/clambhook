#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Install one freshly built GNU/Linux core package inside an explicitly marked
# container, exercise its C daemon and license helper, and uninstall it again.
# The proprietary clambhook-ui package (desktop controller, TUI, Secret Service
# integration) is smoke-tested from the private apps repository. The opt-in
# marker prevents accidental host mutation.
set -euo pipefail

fail() {
    echo "installed-package-smoke: $1" >&2
    exit 1
}

[[ "${CLAMBHOOK_CONTAINER_PACKAGE_SMOKE:-0}" == "1" ]] ||
    fail "refusing to modify a host without CLAMBHOOK_CONTAINER_PACKAGE_SMOKE=1"
[[ "$(uname -s)" == "Linux" ]] || fail "GNU/Linux is required"
[[ "${EUID:-$(id -u)}" == "0" ]] || fail "container root is required"
[[ $# -eq 1 ]] || fail "usage: $0 package.deb|package.rpm"

package_path="$(realpath "$1")"
[[ -f "$package_path" ]] || fail "package does not exist: $package_path"

for tool in curl file gpg readelf strings timeout; do
    command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
done

manager=""
payload=""
case "$package_path" in
    *.deb)
        command -v dpkg-deb >/dev/null 2>&1 || fail "dpkg-deb is required"
        [[ "$(dpkg-deb -f "$package_path" Package)" == "clambhook" ]] ||
            fail "Debian package name is not clambhook"
        package_arch="$(dpkg-deb -f "$package_path" Architecture)"
        host_arch="$(dpkg --print-architecture)"
        [[ "$package_arch" == "$host_arch" ]] ||
            fail "Debian package architecture does not match the host"
        if dpkg-deb -f "$package_path" Depends | grep -Eqi '(default-jre|openjdk|java-runtime)'; then
            fail "Debian runtime dependencies include a JRE"
        fi
        # Minimal Ubuntu containers exclude most documentation at unpack time.
        # Re-include this package's complete notice/license tree so the smoke
        # test validates the payload users receive on a full installation.
        DEBIAN_FRONTEND=noninteractive apt-get \
            -o 'Dpkg::Options::=--path-include=/usr/share/doc/clambhook/*' \
            install -y -qq "$package_path"
        payload="$(dpkg-query -L clambhook)"
        manager="deb"
        ;;
    *.rpm)
        command -v rpm >/dev/null 2>&1 || fail "rpm is required"
        [[ "$(rpm -qp --qf '%{NAME}' "$package_path")" == "clambhook" ]] ||
            fail "RPM package name is not clambhook"
        [[ "$(rpm -qp --qf '%{ARCH}' "$package_path")" == "$(uname -m)" ]] ||
            fail "RPM package architecture does not match the host"
        rpm -qp --qf '%{LICENSE}' "$package_path" |
            grep -Fq 'GPL-3.0-only AND Apache-2.0' ||
            fail "RPM license metadata is incomplete"
        if rpm -qp --requires "$package_path" | grep -Eqi '(jre|jdk|java-runtime)'; then
            fail "RPM runtime dependencies include a JRE"
        fi
        # Fedora container images commonly enable the RPM transaction's nodocs
        # flag. Clear it for this package-contract installation.
        dnf --setopt=tsflags= install -y -q --nogpgcheck "$package_path"
        payload="$(rpm -ql clambhook)"
        manager="rpm"
        ;;
    *) fail "unsupported package type: $package_path" ;;
esac

for installed in /usr/bin/clambhook /usr/bin/clambhook-license \
        /usr/lib/systemd/system/clambhook-daemon.service \
        /usr/share/doc/clambhook/licenses/openssl/LICENSE.txt \
        /usr/share/doc/clambhook/licenses/curl/LICENSE.txt \
        /usr/share/doc/clambhook/licenses/llhttp/LICENSE; do
    [[ -e "$installed" ]] || fail "installed payload is missing $installed"
done
for client in /usr/bin/clambhook-ui /usr/bin/clambhook-tui /usr/lib/clambhook/ui \
        /usr/share/applications/org.jpfchang.clambhook.desktop \
        /usr/share/polkit-1/actions/com.clambhook.Clambhook.policy; do
    [[ ! -e "$client" ]] || fail "client payload belongs in clambhook-ui: $client"
done

# Installed packages must configure the signed repository with the pinned
# developer@jpfchang.org release key.
case "$manager" in
    deb)
        repo_key=/usr/share/keyrings/clambhook-archive-keyring.asc
        repo_config=/etc/apt/sources.list.d/clambhook.sources
        ;;
    rpm)
        repo_key=/etc/pki/rpm-gpg/RPM-GPG-KEY-clambhook
        repo_config=/etc/yum.repos.d/clambhook.repo
        ;;
esac
[[ -f "$repo_key" && -f "$repo_config" ]] ||
    fail "signed repository configuration is missing"
gpg --batch --show-keys --with-colons "$repo_key" 2>/dev/null |
    grep -q '^fpr:::::::::BAFC7769FDA1E0D4EBD23E2F6FF4807EAD977A9B:' ||
    fail "repository key is not the pinned developer@jpfchang.org key"

if printf '%s\n' "$payload" |
        grep -Eqi '(^|/)([^/]*\.go|go\.mod|go\.sum|[^/]*gtk[^/]*|[^/]*javafx[^/]*|[^/]*gluon[^/]*|[^/]*graalvm[^/]*)(/|$)'; then
    fail "retired source or UI payload is installed"
fi
if printf '%s\n' "$payload" | grep -Eq '(^|/)(jre|jdk|runtime)(/|$)'; then
    fail "the core package installs a Java runtime"
fi

for binary in /usr/bin/clambhook /usr/bin/clambhook-license; do
    if readelf -S "$binary" | grep '\.go\.buildinfo' >/dev/null; then
        fail "Go build information remains in $binary"
    fi
    file "$binary" | grep -q "$(uname -m | sed 's/aarch64/ARM aarch64/;s/x86_64/x86-64/')" ||
        fail "$binary architecture does not match the host"
done

license_result="$(printf '%s\n' \
    '{"command":"ensure-trial","snapshot":""}' |
    /usr/bin/clambhook-license)"
printf '%s\n' "$license_result" | grep -q '"ok":true' ||
    fail "installed license helper rejected the frozen trial request"

daemon_pid=""
cleanup_daemon() {
    if [[ -n "$daemon_pid" ]]; then
        kill "$daemon_pid" >/dev/null 2>&1 || true
        wait "$daemon_pid" >/dev/null 2>&1 || true
        daemon_pid=""
    fi
}
trap cleanup_daemon EXIT

install -d -m 0750 /var/lib/clambhook
api_port=19090
api_token="installed-package-smoke"
CLAMBHOOK_API_TOKEN="$api_token" /usr/bin/clambhook \
    -api "127.0.0.1:$api_port" -config /etc/clambhook/config.toml -no-watch \
    >/tmp/clambhook-installed-daemon.log 2>&1 &
daemon_pid=$!

status_file=/tmp/clambhook-installed-status.json
ready=0
for _ in {1..80}; do
    if curl --fail --silent --show-error \
            -H "Authorization: Bearer $api_token" \
            "http://127.0.0.1:$api_port/api/v1/status" \
            >"$status_file" 2>/dev/null; then
        ready=1
        break
    fi
    sleep 0.1
done
if [[ "$ready" != "1" ]]; then
    cat /tmp/clambhook-installed-daemon.log >&2 || true
    fail "installed daemon API did not become ready"
fi
grep -q '"profile":"local"' "$status_file" ||
    fail "installed daemon did not load the packaged profile"

cleanup_daemon
trap - EXIT
case "$manager" in
    deb)
        DEBIAN_FRONTEND=noninteractive apt-get purge -y -qq clambhook
        if dpkg-query -W -f='${db:Status-Status}' clambhook 2>/dev/null |
                grep -Fx 'installed' >/dev/null; then
            fail "Debian package remains registered after purge"
        fi
        ;;
    rpm)
        dnf remove -y -q clambhook
        rpm -q clambhook >/dev/null 2>&1 &&
            fail "RPM remains registered after removal"
        ;;
esac

for removed in /usr/bin/clambhook /usr/bin/clambhook-license \
        /usr/lib/systemd/system/clambhook-daemon.service; do
    [[ ! -e "$removed" ]] || fail "uninstall left package payload at $removed"
done

echo "installed-package-smoke: metadata, install, daemon, license helper, and uninstall passed"
