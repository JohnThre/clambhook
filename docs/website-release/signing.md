# macOS Developer ID Signing

ClambHook keeps `DEVELOPMENT_TEAM` empty in `ui/apple/project.yml` so the
XcodeGen source remains team-agnostic. The public macOS archive is signed,
notarized, stapled, packaged as a DMG, and uploaded to the website artifact
bucket through the macOS release scripts.

## Required Apple Identifiers

Use Apple Developer Team ID `V6GG4HYABJ`.

Create and enable these identifiers in the Apple Developer portal:

- macOS app: `org.jpfchang.clambhook.mac`
- Packet tunnel system extension: `org.jpfchang.clambhook.mac.tunnel`
- macOS widget extension: `org.jpfchang.clambhook.mac.widgets`
- App Group: `group.org.jpfchang.clambhook.mac`
- Keychain group: `V6GG4HYABJ.org.jpfchang.clambhook.mac`
- Privileged helper LaunchDaemon and Mach service:
  `org.jpfchang.clambhook.mac.helper`

Enable these capabilities where applicable:

- Network Extension: `packet-tunnel-provider-systemextension`
- System Extension install entitlement:
  `com.apple.developer.system-extension.install`
- App Groups: `group.org.jpfchang.clambhook.mac`
- Keychain Sharing: `V6GG4HYABJ.org.jpfchang.clambhook.mac`

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
