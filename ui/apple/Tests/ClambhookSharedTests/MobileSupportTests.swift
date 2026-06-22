import XCTest
@testable import ClambhookShared

final class MobileSupportTests: XCTestCase {
    func testPurchaseProductIDsAreStableAndOrdered() {
        XCTAssertEqual(MobilePurchaseCatalog.productIDs, [
            "org.jpfchang.clambhook.unlock.lifetime",
            "org.jpfchang.clambhook.feature_update.2027",
        ])
        XCTAssertEqual(
            MobilePurchaseCatalog.orderedIDs([
                "org.jpfchang.clambhook.feature_update.2027",
                "other",
                "org.jpfchang.clambhook.unlock.lifetime",
            ]),
            [
                "org.jpfchang.clambhook.unlock.lifetime",
                "org.jpfchang.clambhook.feature_update.2027",
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
            [MobilePurchaseCatalog.featureUpdate2027ID]
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
            displayName: "ClambHook for macOS License",
            description: "USD 99.99 direct-sale macOS license includes one year of feature updates; versions released during that year remain usable; up to 4 active Apple Silicon Macs; transferable."
        )
        try assertDirectSaleProduct(
            productsByID[MobilePurchaseCatalog.featureUpdate2027ID],
            displayPrice: "8.99",
            displayName: "ClambHook for macOS 2027 Feature Update",
            description: "Extends the ClambHook macOS feature-update window by one year."
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
