# iPhone App Store Signing

ClambHook keeps `DEVELOPMENT_TEAM` empty in `ui/apple/project.yml` so the XcodeGen source remains team-agnostic. The final iPhone App Store archive is signed by `scripts/archive-ios-app-store.sh` with command-line signing overrides.

## Required Apple Identifiers

Use Apple Developer Team ID `V6GG4HYABJ`.

Create and enable these identifiers in the Apple Developer portal:

- App ID: `org.jpfchang.clambhook`
- Packet tunnel extension App ID: `org.jpfchang.clambhook.tunnel`
- iOS widget extension App ID: `org.jpfchang.clambhook.widgets`
- App Group: `group.org.jpfchang.clambhook`
- Keychain group: `V6GG4HYABJ.org.jpfchang.clambhook`

Enable these capabilities on the app, packet tunnel extension, and iOS widget extension App IDs:

- App Groups: `group.org.jpfchang.clambhook`
- Keychain Sharing: `V6GG4HYABJ.org.jpfchang.clambhook`
- Network Extension: `packet-tunnel-provider`

The widget intentionally keeps Connect, Disconnect, and Next Profile actions. Because those actions use `NETunnelProviderManager`, the widget App ID also needs the Network Extension entitlement for the v1 release.

## Required Environment

The archive script uses App Store Connect API automatic provisioning. Set all variables before archiving:

```sh
export CLAMBHOOK_DEVELOPMENT_TEAM=V6GG4HYABJ
export CLAMBHOOK_APP_STORE_CONNECT_API_KEY_PATH=/path/to/AuthKey_XXXXXXXXXX.p8
export CLAMBHOOK_APP_STORE_CONNECT_API_KEY_ID=XXXXXXXXXX
export CLAMBHOOK_APP_STORE_CONNECT_API_ISSUER_ID=00000000-0000-0000-0000-000000000000
```

Do not commit the API key, private keys, provisioning profiles, or generated archives.
Generated App Store archives and exported IPAs are for App Store Connect submission only. Do not publish `.ipa` files or any other installer/package artifact on GitHub for end users.

## Archive Proof

Run:

```sh
scripts/archive-ios-app-store.sh
```

On success, the script writes:

- `dist/ios/export/*.ipa`
- `dist/ios/signing-proof.txt`

The proof file records resolved build settings and verifies the signed archive/export products for Team ID, bundle IDs, app group, keychain group, and `packet-tunnel-provider`.
