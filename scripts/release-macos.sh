#!/usr/bin/env bash
set -euo pipefail

echo "internal-only: macOS archives are for developer QA/build validation and must not be published on GitHub for end users." >&2

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEAM_ID="${CLAMBHOOK_DEVELOPMENT_TEAM:-}"
NOTARY_PROFILE="${NOTARYTOOL_PROFILE:-}"
DIST_DIR="$ROOT_DIR/dist/macos"
ARCHIVE_PATH="$DIST_DIR/ClambhookMac.xcarchive"
EXPORT_PATH="$DIST_DIR/export"
EXPORT_OPTIONS="$DIST_DIR/ExportOptions.DeveloperID.plist"
APP_PATH="$EXPORT_PATH/ClambhookMac.app"
NOTARY_ZIP="$DIST_DIR/ClambhookMac-notary.zip"
FINAL_ZIP="$DIST_DIR/ClambhookMac-arm64.zip"
FINAL_DMG="$DIST_DIR/ClambhookMac-arm64.dmg"
FINAL_DMG_CHECKSUM="$DIST_DIR/ClambhookMac-arm64.dmg.sha256"
UPDATE_CHANNEL="${UPDATE_CHANNEL:-stable}"
if [[ "$UPDATE_CHANNEL" == "beta" ]]; then
    UPDATE_MANIFEST="$DIST_DIR/clambhook-beta-update-manifest.json"
    APPCAST="$DIST_DIR/appcast-beta.xml"
else
    UPDATE_CHANNEL="stable"
    UPDATE_MANIFEST="$DIST_DIR/clambhook-update-manifest.json"
    APPCAST="$DIST_DIR/appcast.xml"
fi
APPCAST_DOWNLOAD_URL="${CLAMBHOOK_APPCAST_DOWNLOAD_URL:-https://store.clambercloud.com/api/clambhook/download}"
DAEMON="$ROOT_DIR/bin/clambhook"
SODIUM="$ROOT_DIR/bin/libsodium.26.dylib"
HELPER_ENTITLEMENTS="$ROOT_DIR/ui/apple/ClambhookMac/ClambhookDaemon.entitlements"

if [[ -z "$TEAM_ID" ]]; then
    echo "CLAMBHOOK_DEVELOPMENT_TEAM must be set to your paid Apple Developer Team ID." >&2
    exit 1
fi

if [[ -z "$NOTARY_PROFILE" ]]; then
    echo "NOTARYTOOL_PROFILE must be set to an xcrun notarytool keychain profile." >&2
    exit 1
fi

IDENTITY="$(security find-identity -v -p codesigning | awk -v team="($TEAM_ID)" -F '"' '$2 ~ /^Developer ID Application:/ && index($2, team) > 0 { print $2; exit }')"
if [[ -z "$IDENTITY" ]]; then
    echo "No Developer ID Application signing identity found for team $TEAM_ID." >&2
    security find-identity -v -p codesigning >&2
    exit 1
fi

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 make -C "$ROOT_DIR" build-daemon
"$ROOT_DIR/scripts/prepare-macos-runtime.sh"

codesign --force --timestamp --options runtime --sign "$IDENTITY" "$SODIUM"
codesign --force --timestamp --options runtime --entitlements "$HELPER_ENTITLEMENTS" --sign "$IDENTITY" "$DAEMON"
codesign --verify --strict --verbose=4 "$SODIUM"
codesign --verify --strict --verbose=4 "$DAEMON"

cd "$ROOT_DIR/ui/apple"
xcodegen generate --spec project.yml
cd "$ROOT_DIR"

cat > "$EXPORT_OPTIONS" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>method</key>
    <string>developer-id</string>
    <key>signingStyle</key>
    <string>automatic</string>
    <key>signingCertificate</key>
    <string>Developer ID Application</string>
    <key>teamID</key>
    <string>$TEAM_ID</string>
    <key>stripSwiftSymbols</key>
    <true/>
</dict>
</plist>
PLIST

xcodebuild archive \
    -project "$ROOT_DIR/ui/apple/Clambhook.xcodeproj" \
    -scheme ClambhookMac \
    -configuration Release \
    -destination 'generic/platform=macOS' \
    -archivePath "$ARCHIVE_PATH" \
    -allowProvisioningUpdates \
    DEVELOPMENT_TEAM="$TEAM_ID" \
    CODE_SIGN_STYLE=Automatic \
    OTHER_CODE_SIGN_FLAGS="--timestamp"

xcodebuild -exportArchive \
    -archivePath "$ARCHIVE_PATH" \
    -exportOptionsPlist "$EXPORT_OPTIONS" \
    -exportPath "$EXPORT_PATH" \
    -allowProvisioningUpdates \
    DEVELOPMENT_TEAM="$TEAM_ID"

if [[ ! -d "$APP_PATH" ]]; then
    echo "expected exported app at $APP_PATH" >&2
    exit 1
fi

if otool -L "$APP_PATH/Contents/MacOS/clambhook" | grep -q '/opt/homebrew'; then
    echo "exported daemon still contains a Homebrew runtime dependency" >&2
    otool -L "$APP_PATH/Contents/MacOS/clambhook" >&2
    exit 1
fi

"$ROOT_DIR/scripts/check-macos-signing.sh" "$APP_PATH"
codesign --verify --deep --strict --verbose=4 "$APP_PATH"
ditto -c -k --keepParent "$APP_PATH" "$NOTARY_ZIP"
xcrun notarytool submit "$NOTARY_ZIP" --keychain-profile "$NOTARY_PROFILE" --wait
xcrun stapler staple "$APP_PATH"
xcrun stapler validate "$APP_PATH"
spctl -a -vvv -t exec "$APP_PATH"
ditto -c -k --keepParent "$APP_PATH" "$FINAL_ZIP"

echo "Created $FINAL_ZIP"

# Build a signed and notarized DMG.
DMG_STAGING="$DIST_DIR/dmg-staging"
rm -rf "$DMG_STAGING"
mkdir -p "$DMG_STAGING"
cp -R "$APP_PATH" "$DMG_STAGING/"
ln -s /Applications "$DMG_STAGING/Applications"

DMG_RAW="$DIST_DIR/ClambhookMac-arm64-raw.dmg"
hdiutil create \
    -volname "ClambHook" \
    -srcfolder "$DMG_STAGING" \
    -ov \
    -format UDZO \
    -imagekey zlib-level=9 \
    "$DMG_RAW"

codesign --force --timestamp --sign "$IDENTITY" "$DMG_RAW"
codesign --verify --verbose=4 "$DMG_RAW"

DMG_NOTARY_ZIP="$DIST_DIR/ClambhookMac-dmg-notary.zip"
ditto -c -k "$DMG_RAW" "$DMG_NOTARY_ZIP"
xcrun notarytool submit "$DMG_NOTARY_ZIP" --keychain-profile "$NOTARY_PROFILE" --wait
xcrun stapler staple "$DMG_RAW"
xcrun stapler validate "$DMG_RAW"

mv "$DMG_RAW" "$FINAL_DMG"
echo "Created $FINAL_DMG"

# Compute SHA-256 checksum of the DMG.
DMG_SHA256="$(shasum -a 256 "$FINAL_DMG" | awk '{print $1}')"
echo "$DMG_SHA256  ClambhookMac-arm64.dmg" > "$FINAL_DMG_CHECKSUM"
echo "Checksum: $DMG_SHA256"

# GPG-sign the checksum with the configured release key so users (and the
# website) can verify downloads. The update manifest is signed after it is
# generated below. Defaults to the git signing key.
# Pass CLAMBHOOK_GPG_KEY to override. Requires a usable pinentry on the host.
GPG_KEY="${CLAMBHOOK_GPG_KEY:-$(git -C "$ROOT_DIR" config user.signingkey 2>/dev/null || true)}"
if [[ -n "$GPG_KEY" ]] && command -v gpg >/dev/null 2>&1; then
    GPG_BATCH_ARGS=(--batch --yes --pinentry-mode loopback --local-user "$GPG_KEY")
    if gpg "${GPG_BATCH_ARGS[@]}" --detach-sign --armor --output "$FINAL_DMG_CHECKSUM.sig" "$FINAL_DMG_CHECKSUM" 2>/dev/null; then
        echo "GPG-signed checksum with $GPG_KEY → $FINAL_DMG_CHECKSUM.sig"
    else
        echo "Warning: GPG checksum signing failed (no passphrase / agent unavailable). Artifacts are still notarized; skipping .sig." >&2
    fi
else
    echo "Skipping GPG signing: gpg not found or no signing key configured (set CLAMBHOOK_GPG_KEY)." >&2
fi

# Determine build version.
DMG_SIZE="$(stat -f%z "$FINAL_DMG")"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
BUILD_NUMBER="${BUILD_NUMBER:-$(git -C "$ROOT_DIR" rev-list --count HEAD 2>/dev/null || echo '0')}"
SHORT_VERSION="${VERSION}"

# Generate JSON update manifest.
cat > "$UPDATE_MANIFEST" <<JSON
{
  "version": "${SHORT_VERSION}",
  "build": "${BUILD_NUMBER}",
  "channel": "${UPDATE_CHANNEL}",
  "published_at": "${BUILD_DATE}",
  "minimum_os_version": "14.0",
  "url": "https://store.clambercloud.com/api/clambhook/download",
  "filename": "ClambhookMac-arm64.dmg",
  "sha256": "${DMG_SHA256}",
  "size": ${DMG_SIZE}
}
JSON
echo "Created $UPDATE_MANIFEST"

if [[ -n "${GPG_KEY:-}" ]] && command -v gpg >/dev/null 2>&1; then
    GPG_BATCH_ARGS=(--batch --yes --pinentry-mode loopback --local-user "$GPG_KEY")
    gpg "${GPG_BATCH_ARGS[@]}" --detach-sign --armor --output "$UPDATE_MANIFEST.sig" "$UPDATE_MANIFEST" 2>/dev/null \
        && echo "GPG-signed manifest → $UPDATE_MANIFEST.sig" \
        || echo "Warning: manifest GPG signing failed (continuing)." >&2
fi

# Generate a Sparkle appcast with an EdDSA-signed enclosure when Sparkle's
# sign_update tool and private key are available. The signing key is owner-held
# (created with Sparkle's generate_keys) and must never be committed.
SIGN_UPDATE="${SPARKLE_SIGN_UPDATE:-$(command -v sign_update || true)}"
if [[ -n "$SIGN_UPDATE" && -x "$SIGN_UPDATE" ]]; then
    SIGN_ARGS=()
    if [[ -n "${SPARKLE_PRIVATE_KEY_FILE:-}" ]]; then
        SIGN_ARGS+=(--ed-key-file "$SPARKLE_PRIVATE_KEY_FILE")
    fi
    # sign_update prints: sparkle:edSignature="..." length="..."
    SIGN_OUTPUT="$("$SIGN_UPDATE" "${SIGN_ARGS[@]}" "$FINAL_DMG")"
    PUB_DATE="$(date -u "+%a, %d %b %Y %H:%M:%S +0000")"
    cat > "$APPCAST" <<XML
<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle" xmlns:dc="http://purl.org/dc/elements/1.1/">
  <channel>
    <title>ClambHook for macOS</title>
    <link>https://store.clambercloud.com/clambhook/</link>
    <description>ClambHook for macOS updates (${UPDATE_CHANNEL} channel).</description>
    <language>en</language>
    <item>
      <title>ClambHook ${SHORT_VERSION}</title>
      <pubDate>${PUB_DATE}</pubDate>
      <sparkle:version>${BUILD_NUMBER}</sparkle:version>
      <sparkle:shortVersionString>${SHORT_VERSION}</sparkle:shortVersionString>
      <sparkle:minimumSystemVersion>14.0</sparkle:minimumSystemVersion>
      <sparkle:channel>${UPDATE_CHANNEL}</sparkle:channel>
      <enclosure url="${APPCAST_DOWNLOAD_URL}" ${SIGN_OUTPUT} type="application/x-apple-diskimage" />
    </item>
  </channel>
</rss>
XML
    echo "Created $APPCAST"
else
    echo "Skipping appcast generation: Sparkle sign_update not found. Set SPARKLE_SIGN_UPDATE or install Sparkle tools, then re-run." >&2
fi

# Upload to Cloudflare R2 when bucket is configured.
if [[ -n "${CLAMBHOOK_R2_BUCKET:-}" ]]; then
    "$ROOT_DIR/scripts/upload-release-r2.sh" "$FINAL_ZIP" "$FINAL_DMG" "$FINAL_DMG_CHECKSUM" "$UPDATE_MANIFEST" "$APPCAST"
else
    echo "Skipping R2 upload: set CLAMBHOOK_R2_BUCKET and run 'make upload-release-r2' to publish." >&2
fi
