import XCTest
@testable import ClambhookShared

final class MobileSupportTests: XCTestCase {
    func testSupportProductIDsAreStableAndOrdered() {
        XCTAssertEqual(MobileSupportCatalog.productIDs, [
            "support.small",
            "support.medium",
            "support.large",
        ])
        XCTAssertEqual(
            MobileSupportCatalog.orderedIDs(["support.large", "other", "support.small"]),
            ["support.small", "support.large", "other"]
        )
    }
}
