# Website Availability Plan

This is the availability plan for the ClambHook for macOS website distribution
path. It is an operational release plan, not a legal opinion.

## Decision

Distribute the public macOS release only from jpfchang.org and only for users
whose checkout and download access Pengfan Chang intentionally enables.

Do not publish ClambHook installers from GitHub, package registries, public
mirrors, or third-party marketplaces. Do not enable broad automated geographic
expansion without a fresh review.

## Website Setup

- Product page: `https://jpfchang.org/clambhook/`
- Buy page: `https://jpfchang.org/clambhook/buy/`
- License portal: `https://jpfchang.org/clambhook/portal/`
- Download endpoint: `https://jpfchang.org/api/clambhook/download`
- Update manifest endpoint: `https://jpfchang.org/api/clambhook/update-manifest`
- Artifact storage: configured private R2 bucket served through the website

Public copy must send users to jpfchang.org rather than alternate installer
locations.

## Expansion Gate

Before broadening availability, payment coverage, or public distribution:

- Confirm whether local law permits this private VPN/proxy router with metadata
  inspection to be offered in that country or region.
- Confirm whether a VPN, telecom, cybersecurity, encryption, or similar license
  is required.
- If a license is required, obtain and verify it before selecting the territory.
- Record the clearance result, reviewer, review date, and source materials
  before changing website, checkout, or download availability.
- Update public support and policy copy when the distribution scope changes.
- Keep access disabled if clearance is incomplete, disputed, expired, or
  unavailable.

## Clearance Record Template

Use this template for each future expansion decision. Do not commit secrets or
confidential legal work product.

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
Review Notes text:
Source materials:
```
