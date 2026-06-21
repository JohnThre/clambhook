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

        let state = try XCTUnwrap(AppRecoveryStateBuilder.expiredTrial(decision: decision, purchaseAvailability: .available))

        XCTAssertEqual(state.kind, .expiredTrial)
        XCTAssertEqual(state.title, "Free access ended")
        XCTAssertEqual(state.primaryAction, .buyLicense)
        XCTAssertTrue(state.secondaryActions.contains(.activateLicense))
        XCTAssertTrue(state.message.contains("Server-controlled free access ended"))
        XCTAssertTrue(state.message.contains("2026"))
    }

    func testLicenseBackendUnavailableTakesPrecedenceOverExpiredTrialPurchaseState() throws {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)),
            now: mobileLicenseUTCDate(year: 2026, month: 8, day: 4)
        )

        let state = try XCTUnwrap(AppRecoveryStateBuilder.expiredTrial(
            decision: decision,
            purchaseAvailability: .unavailable("License backend is not configured.")
        ))

        XCTAssertEqual(state.kind, .licenseBackendUnavailable)
        XCTAssertEqual(state.primaryAction, .activateLicense)
        XCTAssertEqual(state.diagnosticText, "License backend is not configured.")
    }
}
