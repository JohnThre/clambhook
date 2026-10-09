#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Publish a locally built and signed release to this repository's GitHub
# Releases, where the macOS, Android and GNU/Linux updaters look. Assets come
# from this repository's dist/ (core packages and signed repositories) and the
# private clambhook-apps checkout's dist/ (app installers).
#
#   scripts/publish-release.sh 1.1.0             # dry run: verify and list
#   scripts/publish-release.sh --publish 1.1.0   # stable: needs signed tag v1.1.0
#   scripts/publish-release.sh --publish --beta 1.1.0-beta.1
#
# CLAMBHOOK_APPS_DIR overrides the apps checkout (default ../clambhook-apps).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APPS_DIR="${CLAMBHOOK_APPS_DIR:-$ROOT_DIR/../clambhook-apps}"
REPO="${CLAMBHOOK_RELEASE_REPO:-JohnThre/clambhook}"
KEY="$ROOT_DIR/keys/clambhook-release-key.asc"
EXPECTED_FINGERPRINT="BAFC7769FDA1E0D4EBD23E2F6FF4807EAD977A9B"

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
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] || {
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

asset_dirs=()
for dir in "$ROOT_DIR"/dist/* "$APPS_DIR"/dist/*; do
    [[ -d "$dir" ]] && asset_dirs+=("$dir")
done
(( ${#asset_dirs[@]} > 0 )) || { echo "no dist/ directories with release assets found" >&2; exit 1; }

# Release gate: nothing is uploaded unless every installer, checksum, manifest
# and repository bundle verifies against the pinned key.
"$ROOT_DIR/scripts/verify-release-signatures.sh" "${asset_dirs[@]}"

assets=()
for dir in "${asset_dirs[@]}"; do
    while IFS= read -r -d '' asset; do
        assets+=("$asset")
    done < <(find "$dir" -maxdepth 1 -type f ! -name '.*' -print0 | sort -z)
done

echo "release $tag ($channel) to $REPO:"
printf '  %s\n' "${assets[@]#"$ROOT_DIR"/}"
if (( publish == 0 )); then
    echo "dry run: pass --publish to create the release and upload these assets"
    exit 0
fi

command -v gh >/dev/null 2>&1 || { echo "gh is required to publish" >&2; exit 1; }
if gh release view "$tag" --repo "$REPO" >/dev/null 2>&1; then
    if [[ "$channel" == "beta" ]]; then
        gh release edit "$tag" --repo "$REPO" --title "ClambHook $version" --prerelease --latest=false
    else
        gh release edit "$tag" --repo "$REPO" --title "ClambHook $version" --prerelease=false --latest
    fi
elif [[ "$channel" == "beta" ]]; then
    gh release create "$tag" --repo "$REPO" --title "ClambHook $version" --generate-notes \
        --prerelease --target "$(git rev-parse HEAD)"
else
    gh release create "$tag" --repo "$REPO" --title "ClambHook $version" --generate-notes \
        --verify-tag --latest
fi
gh release upload "$tag" "$KEY" "${assets[@]}" --repo "$REPO" --clobber

if [[ "$channel" == "beta" ]]; then
    if gh release view beta --repo "$REPO" >/dev/null 2>&1; then
        gh release edit beta --repo "$REPO" --title "ClambHook beta" --prerelease --latest=false
    else
        gh release create beta --repo "$REPO" --target "$(git rev-parse HEAD)" \
            --title "ClambHook beta" \
            --notes "Rolling assets for the latest approved ClambHook beta." --prerelease
    fi
    gh release upload beta "$KEY" "${assets[@]}" --repo "$REPO" --clobber
fi
echo "published $tag"
