#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Publish a locally built and signed release to clambercloud.com, the only
# place ClambHook installers are distributed. Files go to the Cloudflare R2
# bucket that the clambercloud.com site streams from:
#
#   releases/<tag>/<file>           -> https://clambercloud.com/clambhook/releases/<tag>/<file>
#   linux/<path>                    -> https://clambercloud.com/clambhook/linux/<path>
#   channels/<channel>/release.json -> the channel pointer the updaters follow
#
# Assets come from this repository's dist/ (core packages and the signed
# apt/dnf repositories) and the private clambhook-apps checkout's dist/ (app
# installers). The channel pointer is written last, so a channel only moves
# once every file it names is in place.
#
#   scripts/publish-release.sh 1.1.0             # dry run: verify and list
#   scripts/publish-release.sh --publish 1.1.0   # stable: needs signed tag v1.1.0
#   scripts/publish-release.sh --publish --beta 1.1.0-beta.1
#
# CLAMBHOOK_APPS_DIR overrides the apps checkout (default ../clambhook-apps),
# CLAMBHOOK_SITE_DIR the clambercloud.com checkout whose Wrangler login is used
# (default ../clambercloud.com), and CLAMBHOOK_R2_BUCKET the bucket
# (default clambhook-artifacts).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APPS_DIR="${CLAMBHOOK_APPS_DIR:-$ROOT_DIR/../clambhook-apps}"
SITE_DIR="${CLAMBHOOK_SITE_DIR:-$ROOT_DIR/../clambercloud.com}"
BUCKET="${CLAMBHOOK_R2_BUCKET:-clambhook-artifacts}"
KEY="$ROOT_DIR/keys/clambhook-release-key.asc"
EXPECTED_FINGERPRINT="BAFC7769FDA1E0D4EBD23E2F6FF4807EAD977A9B"
# shellcheck source=scripts/lib/release-gpg.sh
source "$ROOT_DIR/scripts/lib/release-gpg.sh"

publish=0
channel="stable"
version=""
for argument in "$@"; do
    case "$argument" in
        --publish) publish=1 ;;
        --beta) channel="beta" ;;
        -*) echo "unknown option: $argument" >&2; exit 2 ;;
        *) version="${argument#v}" ;;
    esac
done
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || {
    echo "usage: $0 [--publish] [--beta] VERSION" >&2
    exit 2
}
tag="v$version"
cd "$ROOT_DIR"

[[ -z "$(git status --porcelain)" ]] || { echo "working tree is not clean" >&2; exit 1; }

if [[ "$channel" == "stable" ]]; then
    [[ "$(git cat-file -t "$tag" 2>/dev/null)" == "tag" ]] || {
        echo "stable releases need the annotated, signed tag $tag (scripts/sign-release-tag.sh)" >&2
        exit 1
    }
    verify_home="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-tag-gnupg.XXXXXX")"
    trap 'rm -rf "$verify_home"' EXIT
    chmod 0700 "$verify_home"
    GNUPGHOME="$verify_home" gpg --batch --quiet --import "$KEY"
    fingerprint="$(GNUPGHOME="$verify_home" gpg --batch --with-colons --fingerprint \
        | awk -F: '$1 == "fpr" { print $10; exit }')"
    [[ "$fingerprint" == "$EXPECTED_FINGERPRINT" ]] || {
        echo "pinned release key fingerprint mismatch" >&2
        exit 1
    }
    GNUPGHOME="$verify_home" git verify-tag "$tag"
fi

# Every platform must be present: a release never ships partially.
asset_dirs=()
for dir in "$ROOT_DIR/dist/linux-x86_64" "$ROOT_DIR/dist/linux-aarch64" \
    "$APPS_DIR/dist/linux-ui-x86_64" "$APPS_DIR/dist/linux-ui-aarch64" \
    "$APPS_DIR/dist/macos" "$APPS_DIR/dist/android"; do
    [[ -d "$dir" ]] || { echo "missing release assets: $dir" >&2; exit 1; }
    asset_dirs+=("$dir")
done
repo_tree="$ROOT_DIR/dist/linux-repo/clambhook-linux-repo"
[[ -f "$repo_tree/apt/dists/$channel/InRelease" ]] || {
    echo "missing signed apt/dnf repositories for $channel: $repo_tree" >&2
    exit 1
}

# Release gate: nothing is uploaded unless every installer, checksum, manifest
# and repository bundle verifies against the pinned key.
"$ROOT_DIR/scripts/verify-release-signatures.sh" "${asset_dirs[@]}" "$ROOT_DIR/dist/linux-repo"
# The unpacked repository tree is what gets uploaded: check its signed indexes
# too, not only the bundled tarball.
ch_gpg_verify_clearsigned "$repo_tree/apt/dists/$channel/InRelease"
ch_gpg_verify "$repo_tree/apt/dists/$channel/Release" "$repo_tree/apt/dists/$channel/Release.gpg"
for repomd in "$repo_tree"/rpm/"$channel"/*/repodata/repomd.xml; do
    ch_gpg_verify "$repomd" "$repomd.asc"
done

assets=()
for dir in "${asset_dirs[@]}"; do
    while IFS= read -r -d '' asset; do
        assets+=("$asset")
    done < <(find "$dir" -maxdepth 1 -type f \
        \( -name '*.dmg' -o -name '*.zip' -o -name '*.apk' -o -name '*.aab' \
           -o -name '*.deb' -o -name '*.rpm' -o -name '*.json' -o -name '*.xml' \
           -o -name '*.sha256' -o -name '*.sig' \) \
        ! -name '*notary*' ! -name '.*' -print0 | sort -z)
done
repo_files=()
while IFS= read -r -d '' file; do
    repo_files+=("${file#"$repo_tree"/}")
done < <(find "$repo_tree" -type f ! -name '.*' -print0 | sort -z)

# The channel pointer the site and the updaters resolve.
stage="$(mktemp -d "${TMPDIR:-/tmp}/clambhook-publish.XXXXXX")"
trap 'rm -rf "$stage" "${verify_home:-}"' EXIT
release_json="$stage/release.json"
python3 - "$release_json" "$channel" "$version" "$tag" "${assets[@]}" <<'PY'
import hashlib, json, os, re, sys
from datetime import datetime, timezone

out, channel, version, tag, *paths = sys.argv[1:]
names = {os.path.basename(p): p for p in paths}
if len(names) != len(paths):
    sys.exit("duplicate asset file names across dist directories")
patterns = [
    (r"ClambhookMac-(arm64)\.(dmg|zip)", "macos", "clambhook"),
    (r"ClambHook-(arm64)\.(apk|aab)", "android", "clambhook"),
    (r"ClambHook-UI-" + re.escape(version) + r"-(x86_64|aarch64)\.(deb|rpm)", "linux", "clambhook-ui"),
    (r"ClambHook-" + re.escape(version) + r"-(x86_64|aarch64)\.(deb|rpm)", "linux", "clambhook"),
]
assets = []
for name in sorted(names):
    for pattern, platform, package in patterns:
        match = re.fullmatch(pattern, name)
        if match:
            digest = hashlib.sha256(open(names[name], "rb").read()).hexdigest()
            assets.append({"platform": platform, "package": package, "arch": match.group(1),
                           "kind": match.group(2), "file": name, "sha256": digest})
            break
required = {("macos", "clambhook", "dmg"), ("android", "clambhook", "apk"),
            ("linux", "clambhook", "deb"), ("linux", "clambhook", "rpm"),
            ("linux", "clambhook-ui", "deb"), ("linux", "clambhook-ui", "rpm")}
missing = required - {(a["platform"], a["package"], a["kind"]) for a in assets}
if missing:
    sys.exit(f"release is missing installers: {sorted(missing)}")
beta = channel == "beta"
feeds = {
    "macos-manifest": "clambhook-beta-update-manifest.json" if beta else "clambhook-update-manifest.json",
    "macos-appcast": "appcast-beta.xml" if beta else "appcast.xml",
    "android-manifest": "clambhook-android-manifest.json",
    "linux-manifest-x86_64": "clambhook-linux-x86_64-manifest.json",
    "linux-manifest-aarch64": "clambhook-linux-aarch64-manifest.json",
}
absent = [f for f in feeds.values() if f not in names]
if absent:
    sys.exit(f"release is missing update feeds: {absent}")
document = {
    "schema": 1,
    "channel": channel,
    "version": version,
    "tag": tag,
    "publishedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "assets": assets,
    "feeds": feeds,
}
with open(out, "w") as handle:
    json.dump(document, handle, indent=2)
    handle.write("\n")
PY
ch_gpg_sign "$release_json"
ch_gpg_verify "$release_json"

echo "release $tag ($channel) to R2 bucket $BUCKET (served by clambercloud.com):"
printf '  releases/%s/%s\n' "$tag" "$(basename "$KEY")"
for asset in "${assets[@]}"; do printf '  releases/%s/%s\n' "$tag" "$(basename "$asset")"; done
printf '  linux/%s\n' "${repo_files[@]}"
printf '  channels/%s/release.json{,.sig}\n' "$channel"
if (( publish == 0 )); then
    echo "dry run: pass --publish to upload these files"
    exit 0
fi

wrangler="${CLAMBHOOK_WRANGLER:-$SITE_DIR/node_modules/.bin/wrangler}"
[[ -x "$wrangler" ]] || { echo "wrangler not found: $wrangler (run npm install in $SITE_DIR)" >&2; exit 1; }

content_type() {
    case "$1" in
        *.dmg) echo application/x-apple-diskimage ;;
        *.zip) echo application/zip ;;
        *.apk) echo application/vnd.android.package-archive ;;
        *.deb) echo application/vnd.debian.binary-package ;;
        *.rpm) echo application/x-rpm ;;
        *.json) echo application/json ;;
        *.xml | *.xml.gz) echo application/xml ;;
        *.gz) echo application/gzip ;;
        *.sig | *.asc | */InRelease | */Release.gpg) echo application/pgp-signature ;;
        *.sha256 | */Release | */Packages) echo text/plain ;;
        *) echo application/octet-stream ;;
    esac
}

r2_put() {
    local key="$1" file="$2"
    echo "upload $key"
    (cd "$SITE_DIR" && env -u http_proxy -u https_proxy -u all_proxy \
        -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
        "$wrangler" r2 object put "$BUCKET/$key" --remote \
        --file "$file" --content-type "$(content_type "$key")" >/dev/null)
}

r2_put "releases/$tag/$(basename "$KEY")" "$KEY"
for asset in "${assets[@]}"; do
    r2_put "releases/$tag/$(basename "$asset")" "$asset"
done
# Packages before the signed indexes that list them.
for file in "${repo_files[@]}"; do
    case "$file" in
        apt/dists/* | rpm/*/repodata/*) ;;
        *) r2_put "linux/$file" "$repo_tree/$file" ;;
    esac
done
for file in "${repo_files[@]}"; do
    case "$file" in
        apt/dists/* | rpm/*/repodata/*) r2_put "linux/$file" "$repo_tree/$file" ;;
    esac
done
r2_put "channels/$channel/release.json.sig" "$release_json.sig"
r2_put "channels/$channel/release.json" "$release_json"
echo "published $tag ($channel) to clambercloud.com"
