#!/usr/bin/env bash
# Validate the least-privilege hardening of clambhook-daemon.service and the
# Debian/RPM metadata that provisions its dedicated runtime user.
#
# This is a focused regression check: it fails on the pre-hardening unit (which
# ran as root with NoNewPrivileges=false and no user/sandboxing) and on package
# metadata that forgets to create/own the runtime user's directories. It is
# host-agnostic (pure text assertions) and additionally runs
# `systemd-analyze verify` / `systemd-analyze security` when systemd is present.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UNIT="$ROOT/packaging/systemd/clambhook-daemon.service"
SYSUSERS="$ROOT/packaging/systemd/clambhook-sysusers.conf"
TMPFILES="$ROOT/packaging/systemd/clambhook-tmpfiles.conf"
SPEC="$ROOT/packaging/rpm/clambhook.spec"
DEB_CONTROL="$ROOT/debian/control"
DEB_INSTALL="$ROOT/debian/install"

fail() {
    echo "validate-systemd-unit: FAIL: $*" >&2
    exit 1
}

log() {
    printf 'validate-systemd-unit: %s\n' "$*"
}

# assert_kv KEY VALUE FILE — require a `KEY=VALUE` directive (whitespace-tolerant).
assert_kv() {
    local key="$1" value="$2" file="$3"
    grep -Eq "^[[:space:]]*${key}[[:space:]]*=[[:space:]]*${value}[[:space:]]*$" "$file" \
        || fail "expected '${key}=${value}' in ${file#$ROOT/}"
}

assert_contains() {
    local pattern="$1" file="$2" desc="$3"
    grep -Eq "$pattern" "$file" || fail "$desc (missing /$pattern/ in ${file#$ROOT/})"
}

refute_contains() {
    local pattern="$1" file="$2" desc="$3"
    grep -Eq "$pattern" "$file" && fail "$desc (found /$pattern/ in ${file#$ROOT/})"
    return 0
}

[ -f "$UNIT" ] || fail "unit file not found: $UNIT"

log "checking least-privilege unit directives"

# Runs as a dedicated unprivileged user, not root.
assert_kv User clambhook "$UNIT"
assert_kv Group clambhook "$UNIT"

# NoNewPrivileges MUST be on (regression guard: the old unit had it false).
assert_kv NoNewPrivileges true "$UNIT"

# Exactly the two capabilities TUN routing needs, ambient + bounding, no more.
assert_kv AmbientCapabilities "CAP_NET_ADMIN CAP_NET_RAW" "$UNIT"
assert_kv CapabilityBoundingSet "CAP_NET_ADMIN CAP_NET_RAW" "$UNIT"

# TUN + netlink must remain reachable by construction.
assert_contains '^RestrictAddressFamilies=.*AF_NETLINK' "$UNIT" "netlink family must be allowed for route/TUN setup"
assert_contains '^RestrictAddressFamilies=.*AF_INET( |$)' "$UNIT" "AF_INET must be allowed"
assert_contains '^RestrictAddressFamilies=.*AF_UNIX' "$UNIT" "AF_UNIX must be allowed"
assert_contains '^DeviceAllow=/dev/net/tun rw' "$UNIT" "the tunnel device must be allowed"

# Writable config/state by construction under an otherwise read-only fs.
assert_kv ProtectSystem strict "$UNIT"
assert_kv ConfigurationDirectory clambhook "$UNIT"
assert_kv StateDirectory clambhook "$UNIT"

# Sandbox breadth.
assert_kv ProtectHome true "$UNIT"
assert_kv PrivateTmp true "$UNIT"
assert_kv ProtectKernelModules true "$UNIT"
assert_kv RestrictSUIDSGID true "$UNIT"
assert_contains '^SystemCallFilter=@system-service' "$UNIT" "syscall allow-list must be set"

# Regression guards for TUN/attribution-breaking directives.
refute_contains '^[[:space:]]*PrivateDevices[[:space:]]*=[[:space:]]*(yes|true|1)' "$UNIT" \
    "PrivateDevices=yes would remove /dev/net/tun"
refute_contains '^[[:space:]]*ProcSubset[[:space:]]*=[[:space:]]*pid' "$UNIT" \
    "ProcSubset=pid would hide /proc/net used for connection attribution"
refute_contains '^[[:space:]]*ProtectProc[[:space:]]*=[[:space:]]*invisible' "$UNIT" \
    "ProtectProc=invisible would break per-process connection attribution"
refute_contains '^[[:space:]]*PrivateUsers[[:space:]]*=[[:space:]]*(yes|true|1)' "$UNIT" \
    "PrivateUsers=yes would neutralise CAP_NET_ADMIN on the host network"

log "checking sysusers/tmpfiles provisioning artifacts"
[ -f "$SYSUSERS" ] || fail "missing $SYSUSERS"
[ -f "$TMPFILES" ] || fail "missing $TMPFILES"
assert_contains '^u[[:space:]]+clambhook' "$SYSUSERS" "sysusers must declare the clambhook user"
assert_contains '^d[[:space:]]+/etc/clambhook[[:space:]]+[0-7]+[[:space:]]+clambhook[[:space:]]+clambhook' \
    "$TMPFILES" "tmpfiles must own /etc/clambhook for the runtime user"
assert_contains '^d[[:space:]]+/var/lib/clambhook[[:space:]]+[0-7]+[[:space:]]+clambhook[[:space:]]+clambhook' \
    "$TMPFILES" "tmpfiles must own /var/lib/clambhook for the runtime user"

log "checking RPM metadata"
[ -f "$SPEC" ] || fail "missing $SPEC"
assert_contains 'useradd .*clambhook' "$SPEC" "spec must create the clambhook user in %pre"
assert_contains 'Requires\(pre\):[[:space:]]*shadow-utils' "$SPEC" "spec must pull shadow-utils for %pre"
assert_contains '_sysusersdir./clambhook.conf' "$SPEC" "spec must ship the sysusers.d file"
assert_contains '_tmpfilesdir./clambhook.conf' "$SPEC" "spec must ship the tmpfiles.d file"
assert_contains 'attr\(0750,clambhook,clambhook\) %dir .*sysconfdir./clambhook' "$SPEC" \
    "spec must own the config dir as the runtime user"
assert_contains 'Requires:[[:space:]]*iproute' "$SPEC" "spec must depend on iproute for the ip helper"

log "checking Debian metadata"
[ -f "$DEB_CONTROL" ] || fail "missing $DEB_CONTROL"
[ -f "$DEB_INSTALL" ] || fail "missing $DEB_INSTALL"
assert_contains 'sysusers\.d/' "$DEB_INSTALL" "debian/install must ship the sysusers.d file (dh_installsysusers wires it)"
assert_contains 'tmpfiles\.d/' "$DEB_INSTALL" "debian/install must ship the tmpfiles.d file (dh_installtmpfiles wires it)"
assert_contains '^[[:space:]]*iproute2,' "$DEB_CONTROL" "debian control must depend on iproute2 for the ip helper"

log "static assertions passed"

# Optional live validation when systemd tooling is available.
if command -v systemd-analyze >/dev/null 2>&1; then
    log "running systemd-analyze verify"
    # verify resolves the unit graph; the ExecStart binary may be absent in a
    # checkout, which systemd-analyze reports as a warning, not a hard failure.
    if ! systemd-analyze verify "$UNIT" 2> "$ROOT/.systemd-verify.log"; then
        if grep -Eqv 'Command .* is not executable|Failed to prepare filename|/usr/libexec/clambhook' "$ROOT/.systemd-verify.log"; then
            cat "$ROOT/.systemd-verify.log" >&2
            rm -f "$ROOT/.systemd-verify.log"
            fail "systemd-analyze verify reported unit errors"
        fi
    fi
    rm -f "$ROOT/.systemd-verify.log"

    if systemd-analyze security --help 2>&1 | grep -q -- '--offline'; then
        log "running systemd-analyze security (offline)"
        systemd-analyze security --offline=true "$UNIT" || \
            log "note: systemd-analyze security returned non-zero exposure score (informational)"
    else
        log "note: systemd-analyze security --offline unsupported on this systemd; skipping"
    fi
else
    log "note: systemd-analyze unavailable on this host ($(uname -s)); ran static assertions only"
fi

log "completed"
