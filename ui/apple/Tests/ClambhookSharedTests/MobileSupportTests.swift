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

    func testPurchaseOffersShowLifetimeFirstBeforeUnlock() {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)),
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
        )

        XCTAssertEqual(
            MobilePurchaseCatalog.purchaseOfferIDs(for: decision),
            [MobilePurchaseCatalog.lifetimeUnlockID]
        )
    }

    func testPurchaseOffersHidePaidUpdateWhenLifetimeHasNoLockedFeatures() {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(
                transactions: [
                    MobileLicenseTransaction(
                        productID: MobilePurchaseCatalog.lifetimeUnlockID,
                        purchaseDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
                    ),
                ]
            ),
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
        )

        XCTAssertEqual(MobilePurchaseCatalog.purchaseOfferIDs(for: decision), [])
    }

    func testPurchaseOffersShowPaidUpdateOnlyForLifetimeWithLockedPostCutoffFeatures() {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(
                transactions: [
                    MobileLicenseTransaction(
                        productID: MobilePurchaseCatalog.lifetimeUnlockID,
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

    func testLocalStoreKitConfigurationMatchesPurchaseCatalog() throws {
        let configURL = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .appendingPathComponent("ClambhookProducts.storekit")
        let data = try Data(contentsOf: configURL)
        let config = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(config["type"] as? String, "local")
        XCTAssertEqual(config["version"] as? Int, 3)
        XCTAssertEqual((config["settings"] as? [String: Any])?["_storefront"] as? String, "USA")

        let products = try XCTUnwrap(config["products"] as? [[String: Any]])
        let productsByID = Dictionary(uniqueKeysWithValues: products.compactMap { product -> (String, [String: Any])? in
            guard let productID = product["productID"] as? String else {
                return nil
            }
            return (productID, product)
        })
        XCTAssertEqual(MobilePurchaseCatalog.orderedIDs(productsByID.keys), MobilePurchaseCatalog.productIDs)

        try assertStoreKitProduct(
            productsByID[MobilePurchaseCatalog.lifetimeUnlockID],
            displayPrice: "99.99",
            displayName: "ClambHook Lifetime Unlock",
            description: "Unlocks lifetime mobile access for ClambHook."
        )
        try assertStoreKitProduct(
            productsByID[MobilePurchaseCatalog.featureUpdate2027ID],
            displayPrice: "8.99",
            displayName: "ClambHook 2027 Feature Update",
            description: "Unlocks ClambHook mobile features released in the 2027 update cycle."
        )
    }

    private func assertStoreKitProduct(
        _ product: [String: Any]?,
        displayPrice: String,
        displayName: String,
        description: String
    ) throws {
        let product = try XCTUnwrap(product)
        XCTAssertEqual(product["type"] as? String, "NonConsumable")
        XCTAssertEqual(product["displayPrice"] as? String, displayPrice)
        XCTAssertEqual(product["familyShareable"] as? Bool, true)

        let localizations = try XCTUnwrap(product["localizations"] as? [[String: Any]])
        let englishLocalization = try XCTUnwrap(localizations.first { $0["locale"] as? String == "en_US" })
        XCTAssertEqual(englishLocalization["displayName"] as? String, displayName)
        XCTAssertEqual(englishLocalization["description"] as? String, description)
    }
}
