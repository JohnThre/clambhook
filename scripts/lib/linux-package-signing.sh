#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Embedded OpenPGP signatures for Ubuntu (.deb) and Fedora (.rpm) packages.
# Source after scripts/lib/release-gpg.sh. Verification always uses a scratch
# rpm database or debsig keyring built from the pinned public key only.

[[ -n "${CLAMBHOOK_LINUX_PACKAGE_SIGNING_LOADED:-}" ]] && return 0
CLAMBHOOK_LINUX_PACKAGE_SIGNING_LOADED=1

CLAMBHOOK_DEBSIG_POLICIES="${CLAMBHOOK_DEBSIG_POLICIES:-$CLAMBHOOK_GPG_ROOT/packaging/debsig/policies}"

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
    # rpm 4.x prints "Signature, key ID <subkey-id>: OK"; rpm 6 prints
    # "signature, key fingerprint: <primary-fpr>: OK". The scratch database
    # holds only the pinned key, so either form identifies it.
    signer="(key ID ${CLAMBHOOK_GPG_SIGNING_KEYID}|key fingerprint: (${CLAMBHOOK_GPG_PRIMARY_FPR}|${CLAMBHOOK_GPG_SIGNING_FPR})): OK"
    if ! grep -Eiq "signature, ${signer}" <<<"$output" ||
        grep -Eq '(BAD|NOKEY|NOTTRUSTED|NOT OK)' <<<"$output"; then
        printf '%s\n' "$output" >&2
        ch_gpg_fail "RPM signature verification failed for $package"
    fi
    echo "Verified embedded RPM signature on $package"
}

# ch_deb_sign <package.deb>: add a debsigs "origin" signature in place.
ch_deb_sign() {
    local package="$1" wrapper
    ch_gpg_signing_enabled || ch_gpg_fail "Debian package signing requires REQUIRE_SIGNING=1"
    ch_gpg_require debsigs "Debian package signing"
    wrapper="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-gpg-wrapper.XXXXXX")"
    ch_gpg_wrapper_dir "$wrapper"
    PATH="$wrapper:$PATH" debsigs --sign=origin -k "${GPG_KEY}!" "$package" || {
        rm -rf "$wrapper"
        ch_gpg_fail "debsigs failed for $package"
    }
    rm -rf "$wrapper"
    echo "debsigs-signed $package"
}

# ch_deb_verify <package.deb>: debsig-verify with the committed policy and a
# keyring generated from the pinned public key.
ch_deb_verify() {
    local package="$1" keyrings policy_dir
    ch_gpg_require debsig-verify "Debian package signature verification"
    policy_dir="$CLAMBHOOK_DEBSIG_POLICIES/$CLAMBHOOK_GPG_SIGNING_KEYID"
    [[ -f "$policy_dir/clambhook.pol" ]] || ch_gpg_fail "missing debsig policy in $policy_dir"
    keyrings="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-debsig.XXXXXX")"
    mkdir -p "$keyrings/$CLAMBHOOK_GPG_SIGNING_KEYID"
    ch_gpg_dearmor_public_key "$keyrings/$CLAMBHOOK_GPG_SIGNING_KEYID/debsig.gpg"
    if ! debsig-verify --policies-dir "$CLAMBHOOK_DEBSIG_POLICIES" \
        --keyrings-dir "$keyrings" "$package"; then
        rm -rf "$keyrings"
        ch_gpg_fail "debsig-verify rejected $package"
    fi
    rm -rf "$keyrings"
    echo "Verified embedded debsigs signature on $package"
}

# ch_gpg_dearmor_public_key <output>: binary keyring of the pinned key.
ch_gpg_dearmor_public_key() {
    local output="$1"
    gpg --batch --yes --dearmor --output "$output" "$CLAMBHOOK_RELEASE_PUBLIC_KEY" ||
        ch_gpg_fail "cannot dearmor the pinned public key"
}
