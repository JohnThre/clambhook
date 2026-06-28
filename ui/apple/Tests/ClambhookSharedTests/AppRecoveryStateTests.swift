import XCTest
@testable import ClambhookShared

final class AppRecoveryStateTests: XCTestCase {
    func testNoProfileStateIsActionableWithoutDiagnostic() {
        let state = AppRecoveryStateBuilder.noProfile()

        XCTAssertEqual(state.kind, .missingProfile)
        XCTAssertEqual(state.title, "No profile yet")
        XCTAssertEqual(state.primaryAction, .importProfile)
        XCTAssertTrue(state.secondaryActions.contains(.createProfile))
        XCTAssertEqual(state.diagnosticText, "")
    }

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
        XCTAssertEqual(state.title, "Trial ended")
        XCTAssertEqual(state.primaryAction, .buyLicense)
        XCTAssertTrue(state.secondaryActions.contains(.activateLicense))
        XCTAssertTrue(state.message.contains("The two-month trial ended"))
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

    func testLicenseExpiredForUpdatesUsesCutoffAndRenewalAction() throws {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.macLicenseProductID,
                    purchaseDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
                ),
            ]),
            now: mobileLicenseUTCDate(year: 2027, month: 6, day: 4)
        )

        let state = try XCTUnwrap(AppRecoveryStateBuilder.licenseExpiredForUpdates(
            decision: decision,
            manifestPublishedAt: mobileLicenseUTCDate(year: 2027, month: 6, day: 4)
        ))

        XCTAssertEqual(state.kind, .licenseExpiredForUpdates)
        XCTAssertEqual(state.primaryAction, .renewUpdates)
        XCTAssertTrue(state.message.contains("after your included feature-update window ended"))
        XCTAssertTrue(state.diagnosticText.contains("Bug fixes and security fixes remain included."))
    }

    func testLicenseExpiredForUpdatesIgnoresIncludedRelease() {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.macLicenseProductID,
                    purchaseDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
                ),
            ]),
            now: mobileLicenseUTCDate(year: 2027, month: 6, day: 4)
        )

        XCTAssertNil(AppRecoveryStateBuilder.licenseExpiredForUpdates(
            decision: decision,
            manifestPublishedAt: mobileLicenseUTCDate(year: 2027, month: 6, day: 3)
        ))
    }

    func testPlatformRecoveryStatesUseSpecificActions() throws {
        let certificateState = try XCTUnwrap(AppRecoveryStateBuilder.certificateNotTrusted(fingerprint: "AA:BB"))
        let daemonState = AppRecoveryStateBuilder.daemonFallbackUnavailable(message: "missing binary")

        XCTAssertEqual(certificateState.kind, .certificateNotTrusted)
        XCTAssertEqual(certificateState.primaryAction, .trustCertificate)
        XCTAssertEqual(daemonState.kind, .daemonFallbackUnavailable)
        XCTAssertEqual(daemonState.primaryAction, .launchDaemon)
        XCTAssertEqual(daemonState.diagnosticText, "missing binary")
    }
}
