import XCTest
@testable import ClambhookShared

final class MobileSupportTests: XCTestCase {
    func testSupportProductIDsAreStableAndOrdered() {
        XCTAssertEqual(MobileSupportCatalog.productIDs, [
            "org.jpfchang.clambhook.support.small",
            "org.jpfchang.clambhook.support.medium",
            "org.jpfchang.clambhook.support.large",
        ])
        XCTAssertEqual(
            MobileSupportCatalog.orderedIDs([
                "org.jpfchang.clambhook.support.large",
                "other",
                "org.jpfchang.clambhook.support.small",
            ]),
            [
                "org.jpfchang.clambhook.support.small",
                "org.jpfchang.clambhook.support.large",
                "other",
            ]
        )
    }
}
