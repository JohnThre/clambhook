# macOS Release Runbook (Owner)

End-user macOS installer is distributed **only from store.clambercloud.com**. GitHub stays
source-only and view-only. The installer is Apple Developer ID signed,
notarized, and stapled so Gatekeeper shows no warnings. Release artifacts are
GPG-signed with the configured release key.

This runbook covers the parts that require owner-held secrets. None of these
steps publish anything to GitHub.

## 0. One-time setup

### 0.1 Capabilities and signing (do this in Xcode, not the website)

Team ID: `V6GG4HYABJ`. Bundle IDs are already set in the project; Xcode
registers the App IDs automatically when you sign with the team. You do **not**
need to create App IDs or type bundle IDs into the developer website.

In Xcode, open `ui/apple/Clambhook.xcodeproj`, select each target →
**Signing & Capabilities** → enable **Automatically manage signing** → choose
team `V6GG4HYABJ`. Then **+ Capability** and add only what each target needs:

- **ClambhookMac** (app):
  - **App Groups** → `group.org.jpfchang.clambhook`.
  - **Keychain Sharing** → `V6GG4HYABJ.org.jpfchang.clambhook`.
- **ClambhookMacWidgetExtension**: **App Groups** →
  `group.org.jpfchang.clambhook`.
- **ClambhookMacHelper**: no capabilities (unsandboxed privileged tool).

The entitlements files are the source of truth and are already authored
correctly in the repo. The app target is a **non-sandboxed** Developer ID build
(`ENABLE_APP_SANDBOX: NO`) so it can drive `networksetup` (System Proxy mode)
and `security add-trusted-cert` (HTTPS CA trust) directly; do not add the App
Sandbox capability to **ClambhookMac**. Do not add **System Extension Install**
or **Network Extensions** to any target for this release. Enhanced Mode is
implemented by the privileged daemon using a macOS utun interface.

#### Fallback: developer portal website (only if Xcode automatic signing fails)

If Xcode cannot produce a matching profile automatically, do this **once** in
https://developer.apple.com/account → Certificates, Identifiers & Profiles:

1. **Identifiers** → open the app and widget App IDs (`…mac`, `…mac.widgets`)
   → confirm **App Groups** is enabled where applicable; save.
2. **Profiles** → **+** → **Developer ID Application** → select the App ID →
   select your Developer ID Application certificate → **Generate** →
   **Download**. Double-click the `.provisionprofile` to install it.
3. In Xcode, switch that target to **Manual** signing and select the profile.

Do not request or enable Network Extension capabilities for this release.

### 0.2 Notarytool credentials

Create and store Apple notarization credentials for `notarytool`:

```sh
xcrun notarytool store-credentials "clambhook-notary" \
    --apple-id "developer@jpfchang.org" \
    --team-id "V6GG4HYABJ" \
    --password "<app-specific-password>"
```

Verify:

```sh
xcrun notarytool history --keychain-profile "clambhook-notary" | head
```

### 0.3 Sparkle EdDSA key (for in-app auto-update)

The app's `Info.plist` currently has `SUPublicEDKey` =
`REPLACE_WITH_SPARKLE_ED25519_PUBLIC_KEY`. Generate the key pair with Sparkle's
`generate_keys` (from the Sparkle tools, or the resolved SPM binary):

```sh
# Path to the generate_keys binary from the Sparkle package:
GENERATE_KEYS="$(xcodebuild -project ui/apple/Clambhook.xcodeproj -scheme ClambhookMac -showBuildSettings 2>/dev/null | grep -m1 'BUILT_PRODUCTS_DIR' | awk -F '= ' '{print $2}')"
# Or download sign_update/generate_keys from the Sparkle release.
generate_keys
```

`generate_keys` prints the public EdDSA key and stores the private key in your
login keychain. Put the printed public key into
`ui/apple/ClambhookMac/Info.plist` under `SUPublicEDKey`, replacing the
placeholder. Commit that change. Never commit or export the private key.

If you prefer a key file instead of the keychain, export it to a secured file
and pass `SPARKLE_PRIVATE_KEY_FILE` to `make release-macos`.

### 0.4 GPG release key

Your configured git signing key is `EAA876B70B1832F5` (signing subkey of
`6FF4807EAD977A9B`, Pengfan Chang <developer@jpfchang.org>). Confirm it signs:

```sh
echo test | gpg --batch --yes --pinentry-mode loopback \
    --local-user EAA876B70B1832F5 --detach-sign --armor
```

If you want a different key for releases, set `CLAMBHOOK_GPG_KEY` in your
release shell. Make sure the public key is published to keyservers and listed
on the ClambHook download page so users can verify.

`make release-macos` **requires** signing and fails closed: it aborts unless the
release carries the Developer ID notarization, the `developer@jpfchang.org` GPG
signature (DMG checksum + update manifest), and an EdDSA-signed Sparkle appcast
(needs `sign_update` on `PATH` or `SPARKLE_SIGN_UPDATE`). For an internal-only
build-validation archive that will never be published, set `CLAMBHOOK_SKIP_GPG=1`
to bypass both GPG and appcast signing.

### 0.5 R2 + website

```sh
export CLAMBHOOK_R2_BUCKET=clambhook-artifacts
```

Confirm the `CLAMBHOOK_ARTIFACTS` R2 bucket is bound to the
store.clambercloud.com deployment and the store.swiphtgroup.com D1 DB +
migrations are applied (see
`docs/website-release/commercial-setup.md`).

## 1. Build, sign, notarize, package

From the repo root:

```sh
export CLAMBHOOK_DEVELOPMENT_TEAM=V6GG4HYABJ
export NOTARYTOOL_PROFILE=clambhook-notary
export CLAMBHOOK_R2_BUCKET=clambhook-artifacts
export VERSION="$(git describe --tags --always --dirty)"
make release-macos
```

`make release-macos` (via `scripts/release-macos.sh`) will:

1. Build the Go daemon for darwin/arm64 and prepare the macOS runtime.
2. Developer ID-sign `libsodium.26.dylib` and the daemon.
3. Archive + export the app with automatic provisioning.
4. Verify signing/layout (`check-macos-signing.sh`).
5. Notarize the app zip with `notarytool ... --wait`, staple, and validate.
6. Build, sign, notarize, and staple the DMG.
7. Write the SHA-256 checksum.
8. GPG-sign the checksum and update manifest (`*.sha256.sig`, `*.json.sig`)
   with your configured key.
9. Generate the EdDSA-signed Sparkle appcast (`appcast.xml`).
10. Upload DMG, ZIP, checksum, manifest, signatures, and appcast to R2
    (`upload-release-r2.sh`) when `CLAMBHOOK_R2_BUCKET` is set.

Outputs land under `dist/macos/`:

- `ClambhookMac-arm64.dmg` (signed + notarized + stapled)
- `ClambhookMac-arm64.dmg.sha256` + `.sig`
- `clambhook-update-manifest.json` + `.sig`
- `appcast.xml`
- `ClambhookMac-arm64.zip`

For a beta build: `UPDATE_CHANNEL=beta make release-macos` (writes
`appcast-beta.xml` and the beta manifest).

## 2. Verify "no Apple warnings"

```sh
APP="dist/macos/export/ClambhookMac.app"
codesign --verify --deep --strict --verbose=4 "$APP"
xcrun stapler validate "$APP"
spctl -a -vvv -t exec "$APP"          # expect: "notarized" + "accepted"
xcrun stapler validate "dist/macos/ClambhookMac-arm64.dmg"
```

`spctl` must say `notarized` and `accepted`. If it warns, do not publish;
re-check notarization and stapling.

## 3. Sign the release tag

```sh
./scripts/sign-release-tag.sh "v$VERSION" create   # create + GPG-sign tag at HEAD
# or, if the tag already exists:
./scripts/sign-release-tag.sh "v$VERSION"
git push origin "v$VERSION"
```

GitHub will show the tag as Verified (the public key for
`EAA876B70B1832F5` / `6FF4807EAD977A9B` must be added to your GitHub account).
Do not attach the DMG or any installer artifact to the GitHub release.

## 4. Confirm the website serves the new build

```sh
curl -sI https://store.clambercloud.com/api/clambhook/download | head -1
curl -s https://store.clambercloud.com/api/clambhook/update-manifest | head
curl -s https://store.clambercloud.com/api/clambhook/appcast.xml | head
```

The download endpoint serves the latest notarized DMG from R2; the appcast and
manifest drive in-app Sparkle updates. If you keep the old JSON manifest route,
both it and the appcast must point at the same version.

## 5. User verification (publish on the download page)

Tell users to verify the download against the GPG-signed checksum:

```sh
# 1. Download ClambhookMac-arm64.dmg, ClambhookMac-arm64.dmg.sha256, and
#    ClambhookMac-arm64.dmg.sha256.sig from store.clambercloud.com/clambhook/download/
# 2. Import the Pengfan Chang (developer@jpfchang.org) public key.
# 3. Verify:
gpg --verify ClambhookMac-arm64.dmg.sha256.sig ClambhookMac-arm64.dmg.sha256
shasum -a 256 -c ClambhookMac-arm64.dmg.sha256
```

A successful `gpg --verify` + `shasum -c` confirms the DMG is the exact,
attested build. Opening it on macOS 14+ should show no Gatekeeper warning
because it is Developer ID signed, notarized, and stapled.

## 6. Release checklist

- [ ] Portal identifiers + capabilities registered (app, tunnel, filter, widget,
      helper, App Group, Keychain group).
- [ ] `notarytool` profile `clambhook-notary` stored.
- [ ] `SUPublicEDKey` set in `Info.plist`; Sparkle private key secured.
- [ ] `CLAMBHOOK_DEVELOPMENT_TEAM`, `NOTARYTOOL_PROFILE`, `CLAMBHOOK_R2_BUCKET`
      exported.
- [ ] `make release-macos` completed; `spctl` says notarized + accepted for app
      and DMG.
- [ ] Checksum + manifest GPG-signed; `.sig` files uploaded to R2.
- [ ] Appcast generated and uploaded; website manifest + appcast match.
- [ ] Release tag GPG-signed and pushed (Verified on GitHub).
- [ ] Download/manifest/appcast endpoints return the new version.
- [ ] No installer artifact attached to the GitHub release (source-only).
