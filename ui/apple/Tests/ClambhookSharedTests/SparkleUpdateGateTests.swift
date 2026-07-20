import XCTest
@testable import ClambhookShared

final class SparkleUpdateGateTests: XCTestCase {
    private func trialDecision() -> MobileLicenseDecision {
        MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)),
            now: mobileLicenseUTCDate(year: 2026, month: 6, day: 4)
        )
    }

    private func lockedDecision() -> MobileLicenseDecision {
        MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)),
            now: mobileLicenseUTCDate(year: 2026, month: 8, day: 4)
        )
    }

    func testGateBlocksUntilDecisionProvided() {
        let gate = SparkleUpdateGate()
        XCTAssertFalse(gate.allowsUpdate(publishedAt: nil))
        XCTAssertEqual(gate.feedURLString(), defaultStableAppcastURL.absoluteString)
    }

    func testGateReflectsLatestFeedURLAndDecision() {
        let gate = SparkleUpdateGate()
        gate.update(feedURLString: "https://example.com/appcast.xml", decision: trialDecision())
        XCTAssertEqual(gate.feedURLString(), "https://example.com/appcast.xml")
        XCTAssertTrue(gate.allowsUpdate(publishedAt: mobileLicenseUTCDate(year: 2030, month: 1, day: 1)))

        gate.update(feedURLString: "https://example.com/beta.xml", decision: lockedDecision())
        XCTAssertEqual(gate.feedURLString(), "https://example.com/beta.xml")
        XCTAssertFalse(gate.allowsUpdate(publishedAt: nil))
    }

    func testGateIsSafeUnderConcurrentAccess() {
        let gate = SparkleUpdateGate(decision: trialDecision())
        let iterations = 1_000
        DispatchQueue.concurrentPerform(iterations: iterations) { index in
            if index.isMultiple(of: 2) {
                gate.update(feedURLString: "https://example.com/\(index).xml", decision: trialDecision())
            } else {
                _ = gate.feedURLString()
                _ = gate.allowsUpdate(publishedAt: nil)
            }
        }
        // Reaching here without a data-race crash is the assertion.
        XCTAssertFalse(gate.feedURLString().isEmpty)
    }
}
