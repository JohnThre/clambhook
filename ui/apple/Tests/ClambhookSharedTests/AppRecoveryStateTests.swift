import XCTest
@testable import ClambhookShared

final class AppRecoveryStateTests: XCTestCase {
    func testMissingProfileStateUsesProfileActionsAndDiagnostic() throws {
        let state = try XCTUnwrap(AppRecoveryStateBuilder.missingProfile(readinessMessage: "Replace the placeholder profile before continuing."))

        XCTAssertEqual(state.kind, .missingProfile)
        XCTAssertEqual(state.primaryAction, .createProfile)
        XCTAssertEqual(state.secondaryActions, [.importProfile, .refresh])
        XCTAssertEqual(state.diagnosticText, "Replace the placeholder profile before continuing.")
    }

    func testInvalidVPNProfileStateMapsRecoveryIssueToRebuildAction() throws {
        let issue = TunnelRecoveryClassifier.issue(forRawError: "configuration invalid")
        let state = try XCTUnwrap(AppRecoveryStateBuilder.invalidVPNEntitlementOrProfile(issue: issue))

        XCTAssertEqual(state.kind, .invalidVPNEntitlementOrProfile)
        XCTAssertEqual(state.primaryAction, .rebuildVPNProfile)
        XCTAssertTrue(state.secondaryActions.contains(.openAppSettings))
    }

    func testExpiredTrialStateIncludesTrialEndAndPurchaseActions() throws {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)),
            now: mobileLicenseUTCDate(year: 2026, month: 8, day: 4)
        )

        let state = try XCTUnwrap(AppRecoveryStateBuilder.expiredTrial(decision: decision, storeKitAvailability: .available))

        XCTAssertEqual(state.kind, .expiredTrial)
        XCTAssertEqual(state.primaryAction, .purchaseLifetime)
        XCTAssertTrue(state.secondaryActions.contains(.restorePurchases))
        XCTAssertTrue(state.message.contains("2026"))
    }

    func testStoreKitUnavailableTakesPrecedenceOverExpiredTrialPurchaseState() throws {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)),
            now: mobileLicenseUTCDate(year: 2026, month: 8, day: 4)
        )

        let state = try XCTUnwrap(AppRecoveryStateBuilder.expiredTrial(
            decision: decision,
            storeKitAvailability: .unavailable("Products are not configured.")
        ))

        XCTAssertEqual(state.kind, .storeKitUnavailable)
        XCTAssertEqual(state.primaryAction, .restorePurchases)
        XCTAssertEqual(state.diagnosticText, "Products are not configured.")
    }
}
