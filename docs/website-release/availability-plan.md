# Website Availability Plan

This is the availability plan for the ClambHook direct-download distribution
path. It is an operational release plan, not a legal opinion.

## Decision

Distribute public ClambHook downloads only from `store.clambercloud.com`.
Process checkout, paid updates, and license device management only on
`store.swiphtgroup.com`.

Do not publish ClambHook installers from GitHub, package registries, public
mirrors, or third-party marketplaces. Do not enable broad automated geographic
expansion without a fresh review.

## Website Setup

- Product page: `https://store.clambercloud.com/clambhook/`
- Download endpoint: `https://store.clambercloud.com/api/clambhook/download`
- Update manifest endpoint: `https://store.clambercloud.com/api/clambhook/update-manifest`
- Appcast endpoint: `https://store.clambercloud.com/api/clambhook/appcast.xml`
- Buy page: `https://store.swiphtgroup.com/clambhook/buy/`
- License portal: `https://store.swiphtgroup.com/clambhook/portal/`
- Artifact storage: configured private R2 bucket served through the download host

## Expansion Gate

Before broadening availability, payment coverage, or public distribution:

- Confirm whether local law permits this private VPN/proxy router with metadata inspection to be offered in that country or region.
- Confirm whether a VPN, telecom, cybersecurity, encryption, or similar license is required.
- If a license is required, obtain and verify it before selecting the territory.
- Record the clearance result, reviewer, review date, and source materials before changing website, checkout, or download availability.
- Update public support and policy copy when the distribution scope changes.
- Keep access disabled if clearance is incomplete, disputed, expired, or unavailable.

## Clearance Record Template

```text
Country or region:
Decision: Approved / Blocked / Needs more review
Reviewer:
Review date:
Local-law summary:
License required: Yes / No / Unclear
License holder:
Issuing regulator:
License number:
Effective date:
Expiry date:
Public copy notes:
Source materials:
```
