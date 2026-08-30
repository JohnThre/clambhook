// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

import XCTest
@testable import ClambhookShared

final class MobileSupportTests: XCTestCase {
    func testDonationDestinationsAreExactAndProviderNeutral() {
        XCTAssertEqual(clambHookKoFiURL.absoluteString, "https://ko-fi.com/jpfchang")
        XCTAssertEqual(clambHookLiberapayURL.absoluteString, "https://en.liberapay.com/jpfchang/")
        XCTAssertEqual(clambHookIssueHuntURL.absoluteString, "https://oss.issuehunt.io/u/johnthre")
        XCTAssertEqual(
            clambHookNowPaymentsDonationURL.absoluteString,
            "https://nowpayments.io/donation?api_key=4f798f1e-c93e-456e-8067-b03b200790cd"
        )
    }

    func testPurchaseProductIDsAreStableAndOrdered() {
        XCTAssertEqual(MobilePurchaseCatalog.productIDs, [
            "org.jpfchang.clambhook.unlock.lifetime",
            "org.jpfchang.clambhook.feature_update",
        ])
        XCTAssertEqual(
            MobilePurchaseCatalog.orderedIDs([
                "org.jpfchang.clambhook.feature_update",
                "other",
                "org.jpfchang.clambhook.unlock.lifetime",
            ]),
            [
                "org.jpfchang.clambhook.unlock.lifetime",
                "org.jpfchang.clambhook.feature_update",
                "other",
            ]
        )
    }

    func testPurchaseOffersShowMacLicenseBeforeActivation() {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)),
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
        )

        XCTAssertEqual(
            MobilePurchaseCatalog.purchaseOfferIDs(for: decision),
            [MobilePurchaseCatalog.macLicenseProductID]
        )
    }

    func testPurchaseOffersHidePaidUpdateWhenMacLicenseHasNoLockedFeatures() {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(
                transactions: [
                    MobileLicenseTransaction(
                        productID: MobilePurchaseCatalog.macLicenseProductID,
                        purchaseDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
                    ),
                ]
            ),
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
        )

        XCTAssertEqual(MobilePurchaseCatalog.purchaseOfferIDs(for: decision), [])
    }

    func testPurchaseOffersShowPaidUpdateOnlyForMacLicenseWithLockedPostCutoffFeatures() {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(
                transactions: [
                    MobileLicenseTransaction(
                        productID: MobilePurchaseCatalog.macLicenseProductID,
                        purchaseDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
                    ),
                ]
            ),
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
        )
        let futureFeature = MobileLicenseFeature(
            id: .widgets,
            displayName: "Future Widgets",
            releaseDate: mobileLicenseUTCDate(year: 2027, month: 6, day: 4)
        )

        XCTAssertEqual(
            MobilePurchaseCatalog.purchaseOfferIDs(for: decision, features: [futureFeature]),
            [MobilePurchaseCatalog.featureUpdateProductID]
        )
    }

    func testSignedCompatibilityEntitlementFixtureMatchesPurchaseCatalog() throws {
        let configURL = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .appendingPathComponent("ClambhookProducts.json")
        let data = try Data(contentsOf: configURL)
        let config = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(config["type"] as? String, "signed-entitlement-compatibility")
        XCTAssertEqual(config["version"] as? Int, 2)
        XCTAssertNil(config["paymentProviders"])

        let products = try XCTUnwrap(config["products"] as? [[String: Any]])
        let productsByID = Dictionary(uniqueKeysWithValues: products.compactMap { product -> (String, [String: Any])? in
            guard let productID = product["productID"] as? String else {
                return nil
            }
            return (productID, product)
        })
        XCTAssertEqual(MobilePurchaseCatalog.orderedIDs(productsByID.keys), MobilePurchaseCatalog.productIDs)

        try assertCompatibilityProduct(
            productsByID[MobilePurchaseCatalog.macLicenseProductID],
            displayPrice: "79.99/year",
            displayName: "ClambHook First Paid Term",
            description: "The first verified USD 79.99 annual term creates this compatibility entitlement. It includes releases during the paid term, a perpetual compatible fallback, and up to six active devices."
        )
        try assertCompatibilityProduct(
            productsByID[MobilePurchaseCatalog.featureUpdateProductID],
            displayPrice: "79.99/year",
            displayName: "ClambHook Renewed Paid Term",
            description: "Each later verified annual payment creates this compatibility entitlement and extends the paid-through cutoff by one year."
        )
    }

    private func assertCompatibilityProduct(
        _ product: [String: Any]?,
        displayPrice: String,
        displayName: String,
        description: String
    ) throws {
        let product = try XCTUnwrap(product)
        XCTAssertEqual(product["displayPrice"] as? String, displayPrice)
        XCTAssertEqual(product["displayName"] as? String, displayName)
        XCTAssertEqual(product["description"] as? String, description)
    }
}
