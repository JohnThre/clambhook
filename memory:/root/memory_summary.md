ClambHook GPG release key gap CLOSED (2026-07-14). Key EAA876B70B1832F5 = signing subkey [S] of primary 6FF4807EAD977A9B [C], uid "Pengfan Chang <developer@jpfchang.org>", Ed25519. Primary fpr BAFC 7769 FDA1 E0D4 EBD2  3E2F 6FF4 807E AD97 7A9B; signing subkey fpr F099 90BB E647 C2D4 3F58  D6F0 EAA8 76B7 0B18 32F5. Secret key present locally and can sign (passphrase-protected).

Public key now PUBLISHED on download host: clambercloud.com repo (store.clambercloud.com is a custom domain on same Cloudflare Pages project).
- Key file: clambercloud.com/public/clambhook/clambhook-release-key.asc -> served at /clambhook/clambhook-release-key.asc. Imports cleanly, fingerprints match.
- Constants: clambercloud.com/src/lib/apps.ts (CLAMBHOOK_RELEASE_KEY_URL/UID/FINGERPRINT/SIGNING_SUBKEY).
- UI: clambercloud.com/src/pages/clambhook/download.astro "Verify your download" section (key details, .asc link, gpg import/verify + shasum commands).
- Store cross-ref: swiphtgroup.com/src/lib/clambhook-product.ts (CLAMBHOOK_RELEASE_KEY_URL/FINGERPRINT) + src/pages/clambhook/license.astro "Verify your download" section.
- Docs w/ mermaid diagrams: clambercloud.com/docs/clambhook-release-verification.md, swiphtgroup.com/docs/CLAMBHOOK-RELEASE-VERIFICATION.md.
Verified: both `astro check` = 0 errors; clambercloud commercial check passes; npm run build ok, .asc emitted to dist, section bundled in download chunk.

Prior context (licensing spec, still authoritative): after 1-month trial pay US$99.99 one-time license = 1yr updates + up to 10 devices; renew updates US$9.99/yr; payments Creem + NOWPayments ONLY (no PayPal). Downloads from store.clambercloud.com; checkout/license/portal on store.swiphtgroup.com.

NOTE: store.clambercloud.com currently NOT live in this environment (DNS -> 198.18.25.32 RFC2544 blackhole, TLS EOF). Live-serving the key requires that Cloudflare Pages custom domain to be active + DNS fixed (out of repo scope). Repo-side publication is complete.
