import XCTest
@testable import ClambhookShared

final class MobileSupportTests: XCTestCase {
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

    func testDirectSaleProductFixtureMatchesPurchaseCatalog() throws {
        let configURL = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .appendingPathComponent("ClambhookProducts.json")
        let data = try Data(contentsOf: configURL)
        let config = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(config["type"] as? String, "direct-sale")
        XCTAssertEqual(config["version"] as? Int, 1)
        XCTAssertEqual(config["paymentProviders"] as? [String], ["creem", "nowpayments"])

        let products = try XCTUnwrap(config["products"] as? [[String: Any]])
        let productsByID = Dictionary(uniqueKeysWithValues: products.compactMap { product -> (String, [String: Any])? in
            guard let productID = product["productID"] as? String else {
                return nil
            }
            return (productID, product)
        })
        XCTAssertEqual(MobilePurchaseCatalog.orderedIDs(productsByID.keys), MobilePurchaseCatalog.productIDs)

        try assertDirectSaleProduct(
            productsByID[MobilePurchaseCatalog.macLicenseProductID],
            displayPrice: "99.99",
            displayName: "ClambHook License",
            description: "USD 99.99 one-time ClambHook license after a one-calendar-month trial; includes one year of all updates; versions released on or before the cutoff remain usable; maximum 10 concurrently active devices; deactivatable and transferable."
        )
        try assertDirectSaleProduct(
            productsByID[MobilePurchaseCatalog.featureUpdateProductID],
            displayPrice: "9.99",
            displayName: "ClambHook Update Year",
            description: "USD 9.99 buys one additional update year from the later of the current cutoff or renewal payment date."
        )
    }

    private func assertDirectSaleProduct(
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
