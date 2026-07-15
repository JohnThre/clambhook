ClambHook macOS v1.0.0 (stable/"reliable" channel) SHIPPED to end users on 2026-07-15.

RELEASE FACTS
- Version 1.0.0, build 210, channel stable. Signed git tag v1.0.0 pushed (0c099f0…, GPG EAA876B70B1832F5, Verified).
- DMG sha256 3d02eefc85ecd28f8ae26a760215fd865fbb24f8afd40a95709cb7991a947673, size 16031984, Developer ID (V6GG4HYABJ) notarized+stapled, Gatekeeper accepted.
- Built via `make release-macos` (VERSION=1.0.0 UPDATE_CHANNEL=stable NOTARYTOOL_PROFILE=clambhook-notary). GPG signing done out-of-band via gpg-agent+pinentry-mac because ~/.gnupg/gpg.conf forces pinentry-mode loopback which fails under non-interactive shells; script's loopback gpg cannot get passphrase headless. sign_update at DerivedData/Clambhook-*/SourcePackages/artifacts/sparkle/Sparkle/bin/sign_update; key in login keychain (svce https://sparkle-project.org, acct ed25519).
- Published to R2 bucket clambhook-artifacts. Enabled bucket public r2.dev (pub-8b42a12d150743c0bd5c4bba642e02f7.r2.dev) since no custom domain. Set clambercloud Pages PRODUCTION secrets CLAMBHOOK_STABLE_{DMG,UPDATE_MANIFEST,APPCAST}_URL to those r2.dev object URLs, then rebuilt+deployed clambercloud.com (branch main) so the /api/clambhook/* proxy routes serve them.
- Verified live: clambercloud.com/api/clambhook/{download 302→dmg, update-manifest 200 v1.0.0, appcast.xml 200 signed}. End-user flow OK: download→gpg --verify checksum (Good sig)→shasum -c OK. NOTE: store.clambercloud.com is network-blocked from this sandbox; verified via clambercloud.com (same Pages project/domain).

PAYMENT/PURCHASE (verified working)
- Providers Creem + NOWPayments ONLY; PayPal excluded everywhere in ClambHook scope (client decodes 'paypal'→.unsupported; store enum rejects it live with 400). Prices: license USD 99.99 (9999¢), update year USD 9.99 (999¢); 10 device seats; renewal +1yr from max(cutoff,paymentDate).
- Tests: swiphtgroup.com store 34/34 (vitest clambhook), Apple ui/apple 143/143 (swift test). Live store: buy/license/portal 200, checkout endpoint deployed, paypal rejected, email validation enforced. Production secrets present (CLAMBHOOK_CREEM_*_PRODUCT_ID, CREEM_*, NOWPAYMENTS_*, EMAIL_*).

OUTSTANDING (owner)
- clambercloud.com working tree had uncommitted release-prep (src/lib/apps.ts release-key consts, src/pages/clambhook/download.astro "Verify your download" section, public/clambhook/clambhook-release-key.asc). These were BUILT+DEPLOYED to prod but remain uncommitted in git — owner should commit.
- Beta channel CLAMBHOOK_BETA_* vars not set (out of scope; stable only).
- No GitHub Release/installer artifact attached (policy: source-only on GitHub; end-user delivery via store only).
