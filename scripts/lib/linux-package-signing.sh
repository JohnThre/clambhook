#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Embedded OpenPGP signatures for Ubuntu (.deb) and Fedora (.rpm) packages.
# Source after scripts/lib/release-gpg.sh. Verification always uses a scratch
# rpm database or keyring built from the pinned public key only.

[[ -n "${CLAMBHOOK_LINUX_PACKAGE_SIGNING_LOADED:-}" ]] && return 0
CLAMBHOOK_LINUX_PACKAGE_SIGNING_LOADED=1

# ch_rpm_sign <package.rpm>: add an RPM header signature in place.
ch_rpm_sign() {
    local package="$1" wrapper
    ch_gpg_signing_enabled || ch_gpg_fail "RPM signing requires REQUIRE_SIGNING=1"
    ch_gpg_require rpmsign "RPM package signing"
    wrapper="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-gpg-wrapper.XXXXXX")"
    ch_gpg_wrapper_dir "$wrapper"
    rpmsign --addsign \
        --define "_gpg_name ${GPG_KEY}!" \
        --define "__gpg $wrapper/gpg" \
        --define "_gpg_path ${GNUPGHOME:-$HOME/.gnupg}" \
        "$package" >/dev/null || {
        rm -rf "$wrapper"
        ch_gpg_fail "rpmsign failed for $package"
    }
    rm -rf "$wrapper"
    echo "RPM-signed $package"
}

# ch_rpm_verify <package.rpm>: require digests and a signature by the pinned
# signing subkey, checked against a scratch database holding only that key.
ch_rpm_verify() {
    local package="$1" db output signer
    ch_gpg_require rpmkeys "RPM signature verification"
    db="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-rpmdb.XXXXXX")"
    rpmkeys --dbpath "$db" --import "$CLAMBHOOK_RELEASE_PUBLIC_KEY" || {
        rm -rf "$db"
        ch_gpg_fail "cannot import the pinned key into a scratch RPM database"
    }
    output="$(rpmkeys --dbpath "$db" --checksig -v "$package" 2>&1 || true)"
    rm -rf "$db"
    # rpm 4.x prints "Signature, key ID <subkey-id>: OK" with the long or (on
    # Ubuntu's rpm 4.18) the short key ID; rpm 6 prints "signature, key
    # fingerprint: <primary-fpr>: OK". The scratch database holds only the
    # pinned key, so any of these forms identifies it.
    signer="(key ID (${CLAMBHOOK_GPG_SIGNING_KEYID}|${CLAMBHOOK_GPG_SIGNING_KEYID: -8})|key fingerprint: (${CLAMBHOOK_GPG_PRIMARY_FPR}|${CLAMBHOOK_GPG_SIGNING_FPR})): OK"
    if ! grep -Eiq "signature, ${signer}" <<<"$output" ||
        grep -Eq '(BAD|NOKEY|NOTTRUSTED|NOT OK)' <<<"$output"; then
        printf '%s\n' "$output" >&2
        ch_gpg_fail "RPM signature verification failed for $package"
    fi
    echo "Verified embedded RPM signature on $package"
}

# ch_deb_sign <package.deb>: add a debsigs "origin" signature in place. Hosts
# without debsigs (the maintainer's Mac) build the identical _gpgorigin member
# with gpg and scripts/lib/deb_ar.py.
ch_deb_sign() {
    local package="$1" wrapper
    ch_gpg_signing_enabled || ch_gpg_fail "Debian package signing requires REQUIRE_SIGNING=1"
    if ! command -v debsigs >/dev/null 2>&1; then
        ch_deb_sign_portable "$package"
        return
    fi
    wrapper="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-gpg-wrapper.XXXXXX")"
    ch_gpg_wrapper_dir "$wrapper"
    PATH="$wrapper:$PATH" debsigs --sign=origin -k "${GPG_KEY}!" "$package" || {
        rm -rf "$wrapper"
        ch_gpg_fail "debsigs failed for $package"
    }
    rm -rf "$wrapper"
    echo "debsigs-signed $package"
}

# ch_deb_sign_portable <package.deb>: debsigs origin signature via gpg + ar.
ch_deb_sign_portable() {
    local package="$1" work
    ch_gpg_require python3 "portable Debian package signing"
    work="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-debsig-sign.XXXXXX")"
    python3 "$CLAMBHOOK_GPG_ROOT/scripts/lib/deb_ar.py" origin-payload "$package" "$work/payload" || {
        rm -rf "$work"
        ch_gpg_fail "cannot read Debian package members of $package"
    }
    ch_gpg_run_signer --openpgp --detach-sign --armor --output "$work/_gpgorigin" "$work/payload" || {
        rm -rf "$work"
        ch_gpg_fail "GPG signing failed for $package"
    }
    python3 "$CLAMBHOOK_GPG_ROOT/scripts/lib/deb_ar.py" append "$package" "$work/_gpgorigin" _gpgorigin || {
        rm -rf "$work"
        ch_gpg_fail "cannot embed the origin signature in $package"
    }
    rm -rf "$work"
    echo "debsigs-signed $package (portable)"
}

# ch_deb_verify <package.deb>: check the _gpgorigin member against the pinned
# signing subkey. debsig-verify is not used: it only matches policies against
# primary keys, so it rejects every signature made by the signing subkey.
ch_deb_verify() {
    local package="$1" work
    ch_gpg_require python3 "Debian package signature verification"
    work="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-debsig-verify.XXXXXX")"
    if ! python3 "$CLAMBHOOK_GPG_ROOT/scripts/lib/deb_ar.py" extract "$package" _gpgorigin "$work/_gpgorigin" ||
        ! python3 "$CLAMBHOOK_GPG_ROOT/scripts/lib/deb_ar.py" signed-payload "$package" "$work/payload"; then
        rm -rf "$work"
        ch_gpg_fail "cannot read the origin signature of $package"
    fi
    (ch_gpg_verify "$work/payload" "$work/_gpgorigin") || {
        rm -rf "$work"
        ch_gpg_fail "embedded origin signature rejected for $package"
    }
    rm -rf "$work"
    echo "Verified embedded debsigs signature on $package"
}
