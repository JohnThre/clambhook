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
}
