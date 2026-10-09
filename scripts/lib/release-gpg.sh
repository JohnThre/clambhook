#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Shared GPG signing and verification for every ClambHook installer. Source this
# file; do not execute it. All release scripts sign with the owner-held
# developer@jpfchang.org key and verify against the public key pinned in
# keys/clambhook-release-key.asc, never against the signer's own keyring.
#
# Inputs (environment):
#   GPG_KEY               signing key ID (default: the pinned signing subkey)
#   GPG_PASSPHRASE_FILE   optional passphrase file for loopback pinentry
#   REQUIRE_SIGNING       1 (default) signs; anything else skips signing only
#   CLAMBHOOK_RELEASE_PUBLIC_KEY  override the pinned public key (tests only)
#   CLAMBHOOK_GPG_PRIMARY_FPR / CLAMBHOOK_GPG_SIGNING_FPR  override pins (tests only)

[[ -n "${CLAMBHOOK_RELEASE_GPG_LOADED:-}" ]] && return 0
CLAMBHOOK_RELEASE_GPG_LOADED=1

CLAMBHOOK_GPG_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CLAMBHOOK_GPG_PRIMARY_FPR="${CLAMBHOOK_GPG_PRIMARY_FPR:-BAFC7769FDA1E0D4EBD23E2F6FF4807EAD977A9B}"
CLAMBHOOK_GPG_SIGNING_FPR="${CLAMBHOOK_GPG_SIGNING_FPR:-F09990BBE647C2D43F58D6F0EAA876B70B1832F5}"
CLAMBHOOK_GPG_SIGNING_KEYID="${CLAMBHOOK_GPG_SIGNING_FPR: -16}"
CLAMBHOOK_RELEASE_PUBLIC_KEY="${CLAMBHOOK_RELEASE_PUBLIC_KEY:-$CLAMBHOOK_GPG_ROOT/keys/clambhook-release-key.asc}"
CLAMBHOOK_GPG_EXPIRY_MARGIN_DAYS="${CLAMBHOOK_GPG_EXPIRY_MARGIN_DAYS:-30}"
GPG_KEY="${GPG_KEY:-$CLAMBHOOK_GPG_SIGNING_KEYID}"
REQUIRE_SIGNING="${REQUIRE_SIGNING:-1}"

ch_gpg_fail() {
    echo "release-gpg: $1" >&2
    exit "${2:-1}"
}

ch_gpg_require() {
    command -v "$1" >/dev/null 2>&1 || ch_gpg_fail "$1 is required for $2." 2
}

# ch_gpg_signing_enabled: true when this run must produce signatures.
ch_gpg_signing_enabled() {
    [[ "$REQUIRE_SIGNING" == "1" ]]
}

# ch_gpg_signer_args: print one gpg argument per line for non-interactive
# signing with the forced signing subkey ("!" disables subkey substitution).
ch_gpg_signer_args() {
    printf '%s\n' --batch --yes --pinentry-mode loopback --local-user "${GPG_KEY}!"
    if [[ -n "${GPG_PASSPHRASE_FILE:-}" ]]; then
        [[ -f "$GPG_PASSPHRASE_FILE" ]] || ch_gpg_fail "GPG_PASSPHRASE_FILE does not exist." 2
        printf '%s\n' --passphrase-file "$GPG_PASSPHRASE_FILE"
    fi
}

ch_gpg_run_signer() {
    local -a args=()
    local arg
    while IFS= read -r arg; do
        args+=("$arg")
    done < <(ch_gpg_signer_args)
    gpg "${args[@]}" "$@"
}

# ch_gpg_sign <file> [output]: armored detached signature, default <file>.sig.
ch_gpg_sign() {
    local target="$1" output="${2:-$1.sig}"
    [[ -f "$target" ]] || ch_gpg_fail "cannot sign missing file: $target"
    if ! ch_gpg_signing_enabled; then
        echo "REQUIRE_SIGNING!=1: skipping signature for $target" >&2
        return 0
    fi
    ch_gpg_require gpg "release signing"
    ch_gpg_run_signer --detach-sign --armor --output "$output" "$target" ||
        ch_gpg_fail "GPG signing failed for $target with key $GPG_KEY."
    echo "GPG-signed $target -> $output"
}

# ch_gpg_clearsign <file> <output>: inline clear-signed copy (apt InRelease).
ch_gpg_clearsign() {
    local target="$1" output="$2"
    ch_gpg_signing_enabled || ch_gpg_fail "clear-signing requires REQUIRE_SIGNING=1"
    ch_gpg_require gpg "release signing"
    ch_gpg_run_signer --clearsign --output "$output" "$target" ||
        ch_gpg_fail "GPG clear-signing failed for $target with key $GPG_KEY."
    echo "GPG-clearsigned $target -> $output"
}

# ch_gpg_wrapper_dir <dir>: create <dir>/gpg, a gpg wrapper that injects the
# non-interactive signer options. Tools that call gpg themselves (rpmsign,
# debsigs) use it so the passphrase never appears on a command line.
ch_gpg_wrapper_dir() {
    local dir="$1" real_gpg passphrase_line=""
    real_gpg="$(command -v gpg)" || ch_gpg_fail "gpg is required for release signing." 2
    mkdir -p "$dir"
    if [[ -n "${GPG_PASSPHRASE_FILE:-}" ]]; then
        [[ -f "$GPG_PASSPHRASE_FILE" ]] || ch_gpg_fail "GPG_PASSPHRASE_FILE does not exist." 2
        passphrase_line="--passphrase-file $(printf '%q' "$GPG_PASSPHRASE_FILE")"
    fi
    cat >"$dir/gpg" <<WRAPPER
#!/usr/bin/env bash
exec $(printf '%q' "$real_gpg") --batch --pinentry-mode loopback $passphrase_line "\$@"
WRAPPER
    chmod 0700 "$dir/gpg"
}

# ch_gpg_verify_home: print a fresh GNUPGHOME containing only the pinned
# public key, after checking that key's primary fingerprint.
ch_gpg_verify_home() {
    local home fingerprint
    ch_gpg_require gpg "signature verification"
    [[ -f "$CLAMBHOOK_RELEASE_PUBLIC_KEY" ]] ||
        ch_gpg_fail "pinned public key not found: $CLAMBHOOK_RELEASE_PUBLIC_KEY"
    home="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-verify.XXXXXX")"
    chmod 0700 "$home"
    GNUPGHOME="$home" gpg --batch --quiet --import "$CLAMBHOOK_RELEASE_PUBLIC_KEY" 2>/dev/null ||
        ch_gpg_fail "cannot import pinned public key"
    fingerprint="$(GNUPGHOME="$home" gpg --batch --with-colons --fingerprint \
        "$CLAMBHOOK_GPG_PRIMARY_FPR" 2>/dev/null | awk -F: '$1 == "fpr" { print $10; exit }')"
    [[ "$fingerprint" == "$CLAMBHOOK_GPG_PRIMARY_FPR" ]] ||
        ch_gpg_fail "pinned public key does not have primary fingerprint $CLAMBHOOK_GPG_PRIMARY_FPR"
    printf '%s\n' "$home"
}

# ch_gpg_verify <file> [signature]: require a good, unexpired, unrevoked
# signature made by the pinned signing subkey of the pinned primary key.
ch_gpg_verify() {
    local target="$1" signature="${2:-$1.sig}" home status
    [[ -f "$target" ]] || ch_gpg_fail "cannot verify missing file: $target"
    [[ -f "$signature" ]] || ch_gpg_fail "missing signature for $target: $signature"
    home="$(ch_gpg_verify_home)" || exit $?
    status="$(GNUPGHOME="$home" gpg --batch --status-fd 1 --verify \
        "$signature" "$target" 2>/dev/null || true)"
    rm -rf "$home"
    ch_gpg_check_status "$status" "$target"
}

# ch_gpg_verify_clearsigned <file>: verify an inline clear-signed file.
ch_gpg_verify_clearsigned() {
    local target="$1" home status
    [[ -f "$target" ]] || ch_gpg_fail "cannot verify missing file: $target"
    home="$(ch_gpg_verify_home)" || exit $?
    status="$(GNUPGHOME="$home" gpg --batch --status-fd 1 --verify \
        "$target" 2>/dev/null || true)"
    rm -rf "$home"
    ch_gpg_check_status "$status" "$target"
}

ch_gpg_check_status() {
    local status="$1" target="$2" validsig
    if grep -Eq '^\[GNUPG:\] (EXPKEYSIG|REVKEYSIG|EXPSIG|BADSIG|ERRSIG)' <<<"$status"; then
        ch_gpg_fail "rejected signature for $target: $(grep -E '^\[GNUPG:\] (EXPKEYSIG|REVKEYSIG|EXPSIG|BADSIG|ERRSIG)' <<<"$status" | head -n1)"
    fi
    grep -Eq '^\[GNUPG:\] GOODSIG ' <<<"$status" ||
        ch_gpg_fail "no good signature for $target"
    # VALIDSIG <signing-fpr> <date> <ts> <expire> <ver> <res> <pk-algo> <hash> <class> <primary-fpr>
    validsig="$(awk '$1 == "[GNUPG:]" && $2 == "VALIDSIG" { print $3 " " $12; exit }' <<<"$status")"
    [[ "$validsig" == "$CLAMBHOOK_GPG_SIGNING_FPR $CLAMBHOOK_GPG_PRIMARY_FPR" ]] ||
        ch_gpg_fail "signature for $target is not from the pinned ClambHook signing key (got: ${validsig:-none})"
    echo "Verified $target (signing key $CLAMBHOOK_GPG_SIGNING_FPR)"
}

# ch_gpg_check_expiry: fail when the pinned signing subkey expires within the
# configured margin, so a release never ships signatures that soon go stale.
ch_gpg_check_expiry() {
    local home expires now margin
    home="$(ch_gpg_verify_home)" || exit $?
    expires="$(GNUPGHOME="$home" gpg --batch --with-colons --with-subkey-fingerprints \
        --list-keys "$CLAMBHOOK_GPG_PRIMARY_FPR" 2>/dev/null | awk -F: -v want="$CLAMBHOOK_GPG_SIGNING_FPR" '
            $1 == "sub" { expiry = $7 }
            $1 == "fpr" && $10 == want { print "found:" expiry; exit }')"
    rm -rf "$home"
    [[ "$expires" == found:* ]] ||
        ch_gpg_fail "pinned public key does not contain signing subkey $CLAMBHOOK_GPG_SIGNING_FPR"
    expires="${expires#found:}"
    [[ -n "$expires" ]] || return 0
    now="$(date +%s)"
    margin=$((CLAMBHOOK_GPG_EXPIRY_MARGIN_DAYS * 86400))
    if (( expires <= now + margin )); then
        ch_gpg_fail "signing subkey $CLAMBHOOK_GPG_SIGNING_FPR expires within $CLAMBHOOK_GPG_EXPIRY_MARGIN_DAYS days; extend it and republish keys/clambhook-release-key.asc"
    fi
}

# ch_checksum <artifact>: write <artifact>.sha256 in sha256sum format.
ch_checksum() {
    local artifact="$1" name dir
    name="$(basename "$artifact")"
    dir="$(dirname "$artifact")"
    if command -v sha256sum >/dev/null 2>&1; then
        (cd "$dir" && sha256sum "$name" >"$name.sha256")
    else
        (cd "$dir" && shasum -a 256 "$name" >"$name.sha256")
    fi
}

# ch_checksum_and_sign <artifact>: detached signatures for the installer itself
# and for its checksum file.
ch_checksum_and_sign() {
    local artifact="$1"
    ch_checksum "$artifact"
    ch_gpg_sign "$artifact"
    ch_gpg_sign "$artifact.sha256"
    echo "  sha256: $(awk '{print $1}' "$artifact.sha256")"
}
