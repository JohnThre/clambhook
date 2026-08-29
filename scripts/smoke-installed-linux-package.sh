#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Install one freshly built GNU/Linux package inside an explicitly marked CI
# container, exercise its C daemon/TUI/JavaFX/secret-service integration, and
# uninstall it again. The opt-in marker prevents accidental host mutation.
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

for tool in curl file readelf secret-tool timeout xvfb-run; do
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
        DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$package_path"
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
        dnf install -y -q --nogpgcheck "$package_path"
        payload="$(rpm -ql clambhook)"
        manager="rpm"
        ;;
    *) fail "unsupported package type: $package_path" ;;
esac

for installed in /usr/bin/clambhook /usr/bin/clambhook-tui \
        /usr/bin/clambhook-license /usr/bin/clambhook-ui \
        /usr/lib/systemd/system/clambhook-daemon.service \
        /usr/share/applications/org.jpfchang.clambhook.desktop \
        /usr/share/metainfo/org.jpfchang.clambhook.metainfo.xml; do
    [[ -e "$installed" ]] || fail "installed payload is missing $installed"
done

if printf '%s\n' "$payload" |
        grep -Eqi '(^|/)([^/]*\.go|go\.mod|go\.sum|[^/]*compose[^/]*|[^/]*gtk[^/]*|jre|jdk)(/|$)'; then
    fail "retired source, UI, or Java-runtime payload is installed"
fi

for binary in /usr/bin/clambhook /usr/bin/clambhook-tui \
        /usr/bin/clambhook-license; do
    if readelf -S "$binary" | grep '\.go\.buildinfo' >/dev/null; then
        fail "Go build information remains in $binary"
    fi
    file "$binary" | grep -q "$(uname -m | sed 's/aarch64/ARM aarch64/;s/x86_64/x86-64/')" ||
        fail "$binary architecture does not match the host"
done

ldd_output="$(ldd /usr/bin/clambhook-ui 2>&1 || true)"
printf '%s\n' "$ldd_output" | grep -Eqi '(libjvm|/jre/|/jdk/)' &&
    fail "the JavaFX native image depends on a JRE"

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

CLAMBHOOK_API_TOKEN="$api_token" /usr/bin/clambhook-tui \
    "127.0.0.1:$api_port" >/tmp/clambhook-installed-tui.log 2>&1 || {
        cat /tmp/clambhook-installed-tui.log >&2 || true
        fail "installed TUI could not read the daemon control API"
    }

ui_config="$(mktemp -d /tmp/clambhook-installed-ui.XXXXXX)"
set +e
timeout 5s xvfb-run -a env \
    XDG_CONFIG_HOME="$ui_config" \
    CLAMBHOOK_API_URL="http://127.0.0.1:$api_port" \
    CLAMBHOOK_API_TOKEN="$api_token" \
    /usr/bin/clambhook-ui >/tmp/clambhook-installed-javafx.log 2>&1
ui_status=$?
set -e
[[ "$ui_status" == "124" ]] || {
    cat /tmp/clambhook-installed-javafx.log >&2 || true
    fail "installed JavaFX controller did not remain healthy during launch smoke"
}

command -v dbus-run-session >/dev/null 2>&1 ||
    fail "dbus-run-session is required for secret storage smoke"
command -v gnome-keyring-daemon >/dev/null 2>&1 ||
    fail "gnome-keyring-daemon is required for secret storage smoke"
secret_home="$(mktemp -d /tmp/clambhook-secret-home.XXXXXX)"
secret_runtime="$(mktemp -d /tmp/clambhook-secret-runtime.XXXXXX)"
chmod 0700 "$secret_home" "$secret_runtime"
# shellcheck disable=SC2016 # Expanded by the nested D-Bus session shell.
if ! timeout 20s env HOME="$secret_home" XDG_RUNTIME_DIR="$secret_runtime" \
        dbus-run-session -- bash -euo pipefail -c '
    printf "%s\n" "clambhook-keyring-smoke" |
        gnome-keyring-daemon --unlock --components=secrets >/dev/null
    printf "%s" "clambhook-secret-smoke" |
        secret-tool store --label="ClambHook package smoke" \
            service clambhook account package-smoke
    test "$(secret-tool lookup service clambhook account package-smoke)" = \
        "clambhook-secret-smoke"
    secret-tool clear service clambhook account package-smoke
'; then
    rm -rf "$secret_home" "$secret_runtime"
    fail "installed Secret Service integration failed"
fi
rm -rf "$secret_home" "$secret_runtime"

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

for removed in /usr/bin/clambhook /usr/bin/clambhook-tui \
        /usr/bin/clambhook-license /usr/bin/clambhook-ui \
        /usr/lib/systemd/system/clambhook-daemon.service \
        /usr/share/applications/org.jpfchang.clambhook.desktop \
        /usr/share/metainfo/org.jpfchang.clambhook.metainfo.xml \
        /usr/share/icons/hicolor/1024x1024/apps/org.jpfchang.clambhook.png \
        /usr/share/polkit-1/actions/com.clambhook.Clambhook.policy; do
    [[ ! -e "$removed" ]] || fail "uninstall left package payload at $removed"
done

echo "installed-package-smoke: metadata, install, daemon, TUI, JavaFX, secret storage, and uninstall passed"
