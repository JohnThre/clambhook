import XCTest
@testable import ClambhookShared

final class LicensingTests: XCTestCase {
    func testTrialLastsTwoCalendarMonths() {
        let start = mobileLicenseUTCDate(year: 2026, month: 1, day: 31)
        let snapshot = MobileLicenseSnapshot(trialStartDate: start)

        let beforeExpiry = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 3, day: 30)
        )
        XCTAssertEqual(beforeExpiry.reason, .trial)
        XCTAssertTrue(beforeExpiry.canUseFeature(.tunnelRouting))

        let afterExpiry = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 4, day: 1)
        )
        XCTAssertEqual(afterExpiry.reason, .locked)
        XCTAssertFalse(afterExpiry.canUseApp)
    }

    func testLifetimeUnlockRemainsUsableWithoutRecentVerification() {
        let purchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.lifetimeUnlockID, purchaseDate: purchaseDate),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2026, month: 6, day: 10)
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2028, month: 6, day: 18)
        )
        XCTAssertEqual(decision.reason, .lifetime)
        XCTAssertTrue(decision.canUseApp)
        XCTAssertEqual(decision.updateCutoffDate, mobileLicenseUTCDate(year: 2027, month: 6, day: 3))
        XCTAssertTrue(decision.canUseFeature(.tunnelRouting))
    }

    func testVerificationFailureDoesNotExpireLifetimeUnlock() {
        let purchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.lifetimeUnlockID, purchaseDate: purchaseDate),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2026, month: 6, day: 10),
            lastVerificationFailedAt: mobileLicenseUTCDate(year: 2026, month: 6, day: 12)
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2028, month: 6, day: 14)
        )
        XCTAssertEqual(decision.reason, .lifetime)
        XCTAssertTrue(decision.canUseApp)
        XCTAssertFalse(decision.isOfflineGraceActive)
    }

    func testRevokedLifetimeDoesNotUnlock() {
        let purchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.lifetimeUnlockID,
                    purchaseDate: purchaseDate,
                    revocationDate: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
                ),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 2)
        )
        XCTAssertEqual(decision.reason, .locked)
        XCTAssertFalse(decision.hasLifetimeUnlock)
    }

    func testPaidUpdatesExtendFeatureWindow() throws {
        let lifetimeDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.lifetimeUnlockID, purchaseDate: lifetimeDate),
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.featureUpdate2027ID, purchaseDate: mobileLicenseUTCDate(year: 2027, month: 8, day: 1)),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2027, month: 8, day: 1)
        )
        let futureFeature = MobileLicenseFeature(
            id: .widgets,
            displayName: "Future Widgets",
            releaseDate: mobileLicenseUTCDate(year: 2028, month: 7, day: 31)
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            features: [futureFeature],
            now: mobileLicenseUTCDate(year: 2027, month: 8, day: 2)
        )
        XCTAssertEqual(decision.reason, .lifetime)
        XCTAssertEqual(decision.updateCutoffDate, mobileLicenseUTCDate(year: 2028, month: 8, day: 1))
        XCTAssertTrue(decision.canUseFeature(.widgets))
    }

    func testPaidUpdateWithoutLifetimeDoesNotUnlock() {
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.featureUpdate2027ID, purchaseDate: mobileLicenseUTCDate(year: 2027, month: 8, day: 1)),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2027, month: 8, day: 1)
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2027, month: 8, day: 2)
        )
        XCTAssertEqual(decision.reason, .locked)
        XCTAssertFalse(decision.canUseApp)
    }
}
