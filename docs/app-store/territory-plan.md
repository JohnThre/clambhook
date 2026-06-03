# VPN Territory Plan

This is the App Store territory plan for the v1 iPhone release. It is an operational submission plan, not a legal opinion.

## Decision

Release v1 in the United States only.

Do not select `All Countries or Regions` for v1. Do not enable automatic availability in future App Store countries or regions.

## App Store Connect Setup

In App Store Connect, use:

`Pricing and Availability` -> `App Availability` -> `Specific Countries or Regions` -> `United States`

All other countries and regions remain deselected until they pass VPN licensing review.

## Review Notes Text

Paste this territory statement into the App Review Notes field with the demo profile credentials:

```text
Territory availability: ClambHook v1 is submitted with App Store availability set to Specific Countries or Regions: United States only. No territories requiring VPN license information are selected for this submission. If future availability expands to a territory requiring a VPN license, we will provide the license holder, issuing regulator, license number, effective and expiry dates, and covered territory in App Review Notes before submitting that build.
```

## Expansion Gate

Before adding any country or region in App Store Connect:

- Confirm whether local law permits this private VPN/proxy router with metadata inspection to be offered in that country or region.
- Confirm whether a VPN, telecom, cybersecurity, encryption, or similar license is required.
- If a license is required, obtain and verify it before selecting the territory.
- Record the clearance result, reviewer, review date, and source materials before changing App Store Connect availability.
- Update App Review Notes with license details for every selected territory that requires license information.
- Keep the territory deselected if clearance is incomplete, disputed, expired, or unavailable.

## Clearance Record Template

Use this template for each future expansion decision. Do not commit secrets or confidential legal work product.

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

## References

- Apple App Review Guideline 5.4, VPN Apps: https://developer.apple.com/app-store/review/guidelines/
- App Store Connect availability setup: https://developer.apple.com/help/app-store-connect/manage-your-apps-availability/manage-availability-for-your-app-on-the-app-store
