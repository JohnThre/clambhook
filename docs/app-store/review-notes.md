# App Review Notes

ClambHook is a private iPhone VPN/proxy router with metadata inspection, distributed only through the Apple App Store. It uses Network Extension with a packet tunnel provider to route device network traffic according to user-managed profiles and rules.

Supported v1 profile protocol identifiers: `clambback`, `openvpn`, `shadowsocks`, `tor`, `trojan`, and `wireguard`.

Privacy posture:

- ClambHook does not sell, use, or disclose VPN traffic data to third parties.
- Profile data, connection metadata, logs, and diagnostics stay on device unless the user exports them.
- v1 inspection is metadata-only: connection targets, routing decisions, byte counts, timing, and hop status.
- ClambHook does not install a certificate authority, perform TLS MITM, store request or response bodies, export HAR files, or provide body-level redaction workflows.
- Apple diagnostics may include crash and performance data if the user has enabled sharing diagnostics with developers.
- ClambHook does not include third-party analytics, advertising SDKs, or tracking SDKs.

Demo profile for App Review:

- Profile name: App Review Demo
- Endpoint: review-vpn.jpfchang.org:443
- Credentials: provide only in the App Review Notes field in App Store Connect.

Initial availability and VPN licensing plan:

- App Store availability for v1: `Specific Countries or Regions` -> `United States` only.
- Do not select `All Countries or Regions` or automatic availability in future App Store countries or regions for v1.
- No territories requiring VPN license information are selected for this submission.
- Any future country or region expansion must pass VPN licensing review before App Store Connect availability is changed.
- If any selected territory requires VPN license information, provide the license holder, issuing regulator, license number, effective and expiry dates, and covered territory in App Review Notes before submission.
- Territory plan: `docs/app-store/territory-plan.md`

App Store Connect URLs:

- Privacy Policy URL: https://jpfchang.org/clambhook/privacy
- Support URL: https://jpfchang.org/clambhook/support
