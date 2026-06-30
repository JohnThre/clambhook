# macOS Developer ID Signing

ClambHook keeps `DEVELOPMENT_TEAM` empty in `ui/apple/project.yml` so the
XcodeGen source remains team-agnostic. The public macOS archive is signed,
notarized, stapled, packaged as a DMG, and uploaded to the website artifact
bucket through the macOS release scripts.

## Required Apple Identifiers

Use Apple Developer Team ID `V6GG4HYABJ`.

Create and enable these identifiers in the Apple Developer portal:

- macOS app: `org.jpfchang.clambhook.mac`
- macOS widget extension: `org.jpfchang.clambhook.mac.widgets`
- App Group: `group.org.jpfchang.clambhook`
- Keychain group: `V6GG4HYABJ.org.jpfchang.clambhook`
- Privileged helper LaunchDaemon and Mach service:
  `org.jpfchang.clambhook.mac.helper`

Enable these capabilities where applicable:

- App Groups: `group.org.jpfchang.clambhook`
- Keychain Sharing: `V6GG4HYABJ.org.jpfchang.clambhook`

Do not enable Network Extensions or System Extension Install for this release.
Enhanced Mode is implemented by the privileged daemon with a macOS utun
interface, so it does not use Apple's restricted Network Extension entitlement.

## Required Environment

Set these variables before building a notarized macOS release:

```sh
export CLAMBHOOK_DEVELOPMENT_TEAM=V6GG4HYABJ
export NOTARYTOOL_PROFILE=clambhook-notary
export CLAMBHOOK_R2_BUCKET=clambhook-artifacts
```

Do not commit private keys, notary credentials, provisioning profiles, generated
archives, exported apps, ZIPs, DMGs, checksums, or update manifests.

## Release Proof

Run:

```sh
make release-macos
```

On success, the script writes release artifacts under `dist/macos/`, including:

- `ClambhookMac-arm64.dmg`
- `ClambhookMac-arm64.dmg.sha256`
- `ClambhookMac-arm64.zip`
- `clambhook-update-manifest.json` or `clambhook-beta-update-manifest.json`

`make release-macos` verifies signing, notarization, stapling, Gatekeeper
assessment, checksum generation, and update manifest generation before optional
R2 upload.

## Sparkle Auto-Update

The macOS app uses Sparkle for in-app download and install of updates. The app
checks the EdDSA-signed appcast served from store.clambercloud.com and gates feature
updates to the buyer's license update window (bug and security fixes remain
available).

### One-time key setup (owner-held secrets)

1. Generate the EdDSA key pair with Sparkle's `generate_keys`. The private key
   is stored in your login keychain; the tool prints the public key.
2. Put the printed public key in `ui/apple/ClambhookMac/Info.plist` under
   `SUPublicEDKey`, replacing `REPLACE_WITH_SPARKLE_ED25519_PUBLIC_KEY`.
3. Never commit the private key. Keep it in the keychain (or a secured key file
   referenced by `SPARKLE_PRIVATE_KEY_FILE`).

### Release wiring

- `make release-macos` calls Sparkle's `sign_update` (found on `PATH` or via
  `SPARKLE_SIGN_UPDATE`) to sign the DMG and writes `appcast.xml`
  (`appcast-beta.xml` on the beta channel) under `dist/macos/`.
- `make upload-release-r2` uploads the appcast to R2 as `appcast.xml` /
  `appcast-beta.xml`.
- The website serves it at `https://store.clambercloud.com/api/clambhook/appcast.xml`
  (and `?channel=beta`), which matches the app's `SUFeedURL`.

### Feed and entitlements

- App `Info.plist`: `SUFeedURL`, `SUPublicEDKey`,
  `SUEnableInstallerLauncherService`, `SUEnableAutomaticChecks`.
- Sandbox mach-lookup temporary exceptions for Sparkle's XPC services
  (`$(PRODUCT_BUNDLE_IDENTIFIER)-spks` and `-spki`) are in
  `ClambhookMac.entitlements`.
