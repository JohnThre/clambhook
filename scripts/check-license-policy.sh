#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

set -euo pipefail

ROOT_DIR="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$ROOT_DIR"

fail() {
    echo "license policy: $1" >&2
    exit 1
}

sha256() {
    shasum -a 256 "$1" | awk '{print $1}'
}

[[ "$(sha256 LICENSE)" == "3972dc9744f6499f0f9b2dbf76696f2ae7ad8af9b23dde66d6af86c9dfb36986" ]] ||
    fail "LICENSE is not the canonical GPLv3 text"
[[ "$(sha256 LICENSE-APACHE)" == "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30" ]] ||
    fail "LICENSE-APACHE is not the canonical Apache-2.0 text"
cmp -s LICENSE-APACHE clib/LICENSE || fail "clib/LICENSE differs from LICENSE-APACHE"

is_mapped() {
    case "$1" in
        .pi-lens.json|clambhook-icon-1024.png|flake.lock|NOTICE|debian/changelog|debian/source/format|keys/clambhook-release-key.asc|packaging/sbom.cdx.json) return 0 ;;
        packaging/icons/*.png|third_party/libmaxminddb/testdata/*.mmdb) return 0 ;;
        ui/android/gradlew|ui/android/gradle/wrapper/*) return 0 ;;
        ui/android/app/src/main/res/*.png|ui/apple/*.png|ui/apple/*.json|ui/apple/*.pbxproj|ui/apple/*.resolved|ui/apple/*.xcworkspacedata|ui/apple/*.xcscheme) return 0 ;;
    esac
    return 1
}

license_text() {
    case "$1" in
        LICENSE|LICENSE-APACHE|clib/LICENSE) return 0 ;;
    esac
    return 1
}

missing=0
while IFS= read -r path; do
    [[ -e "$path" ]] || continue
    case "$path" in
        third_party/*) continue ;;
    esac
    if license_text "$path" || is_mapped "$path"; then
        continue
    fi

    expected="GPL-3.0-only"
    case "$path" in
        clib/*) expected="Apache-2.0" ;;
    esac

    if ! grep -Fq "SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>" "$path" 2>/dev/null; then
        echo "license policy: missing copyright declaration: $path" >&2
        missing=1
    fi
    if ! grep -Fq "SPDX-License-Identifier: $expected" "$path" 2>/dev/null; then
        echo "license policy: expected $expected declaration: $path" >&2
        missing=1
    fi
done < <(git ls-files --cached --others --exclude-standard | LC_ALL=C sort)
[[ "$missing" -eq 0 ]] || exit 1

if rg -n \
    'github\.com/JohnThre/clambhook/(internal|pkg/mobile)|Clambhook::Core|native/(include|src)' \
    clib \
    --glob '!LICENSE'; then
    fail "Apache-2.0 library code depends on the GPL-3.0-only application core"
fi

if rg -ni \
    'Clambhook-Proprietary-View-Only|proprietary source-available|source-only and view-only|proprietary, view-only|confidential and proprietary' \
    README.md SECURITY.md LICENSING.md docs/distribution.md \
    docs/website-release/release-runbook.md \
    docs/website-release/linux-release-runbook.md flake.nix debian/copyright \
    packaging/rpm/clambhook.spec ui/android/app/src/main/res/values/strings.xml \
    ui/apple/ClambhookMac/MacLegalFooter.swift \
    packaging/desktop/org.jpfchang.clambhook.metainfo.xml.in; then
    fail "obsolete proprietary/view-only language remains in current legal surfaces"
fi

grep -Fq 'license = licenses.gpl3Only;' flake.nix || fail "Nix metadata is not GPL-3.0-only"
grep -Fq '<project_license>GPL-3.0-only</project_license>' \
    packaging/desktop/org.jpfchang.clambhook.metainfo.xml.in || fail "AppStream metadata is not GPL-3.0-only"
grep -Fq 'License:        GPL-3.0-only AND Apache-2.0' packaging/rpm/clambhook.spec ||
    fail "RPM metadata does not declare GPL-3.0-only and Apache-2.0"
grep -Fq 'Maintainer: Pengfan Chang <support@swiphtgroup.com>' debian/control ||
    fail "Debian maintainer identity is stale"
grep -Fq 'License: GPL-3.0-only' debian/copyright || fail "Debian GPL declaration is missing"
grep -Fq 'License: Apache-2.0' debian/copyright || fail "Debian Apache declaration is missing"
python3 scripts/check-sbom.py

echo "license policy: all checks passed"
