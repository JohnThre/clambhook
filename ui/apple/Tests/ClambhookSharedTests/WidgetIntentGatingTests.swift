import XCTest
@testable import ClambhookShared

/// The widget App Intents (Connect / Disconnect / NextProfile) all call
/// `WidgetLicenseActionPolicy.requireAllowed` before performing work. These
/// tests pin the throwing behavior those intents rely on.
final class WidgetIntentGatingTests: XCTestCase {
    private func lockedDecision() -> MobileLicenseDecision {
        MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)),
            now: mobileLicenseUTCDate(year: 2026, month: 8, day: 4)
        )
    }

    private func unlockedDecision() -> MobileLicenseDecision {
        MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)),
            now: mobileLicenseUTCDate(year: 2026, month: 6, day: 4)
        )
    }

    func testLockedLicenseBlocksConnectAndNextProfileButAllowsDisconnect() {
        let decision = lockedDecision()
        XCTAssertThrowsError(try WidgetLicenseActionPolicy.requireAllowed(.connect, decision: decision)) { error in
            guard case MobileLicenseRuntimeError.locked = error else {
                return XCTFail("expected MobileLicenseRuntimeError.locked, got \(error)")
            }
        }
        XCTAssertThrowsError(try WidgetLicenseActionPolicy.requireAllowed(.nextProfile, decision: decision))
        XCTAssertNoThrow(try WidgetLicenseActionPolicy.requireAllowed(.disconnect, decision: decision))
    }

    func testUnlockedLicensePermitsEveryWidgetIntent() {
        let decision = unlockedDecision()
        XCTAssertNoThrow(try WidgetLicenseActionPolicy.requireAllowed(.connect, decision: decision))
        XCTAssertNoThrow(try WidgetLicenseActionPolicy.requireAllowed(.disconnect, decision: decision))
        XCTAssertNoThrow(try WidgetLicenseActionPolicy.requireAllowed(.nextProfile, decision: decision))
    }
}
