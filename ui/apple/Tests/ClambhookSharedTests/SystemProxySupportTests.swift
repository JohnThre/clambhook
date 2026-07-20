import XCTest
@testable import ClambhookShared

final class SystemProxySupportTests: XCTestCase {
    func testProxyStateParsesNetworksetupOutput() {
        let state = MacProxyState(output: "Enabled: Yes\nServer: 10.0.0.1\nPort: 8080\nAuthenticated Proxy Enabled: 0")
        XCTAssertTrue(state.enabled)
        XCTAssertEqual(state.server, "10.0.0.1")
        XCTAssertEqual(state.port, 8080)
    }

    func testSnapshotEncodeDecodeRoundTrips() throws {
        let snapshot = [
            MacProxyServiceSnapshot(
                service: "Wi-Fi",
                web: MacProxyState(enabled: true, server: "1.1.1.1", port: 3128),
                secureWeb: MacProxyState(enabled: false, server: "", port: 0),
                socks: MacProxyState(enabled: true, server: "127.0.0.1", port: 1080)
            )
        ]
        let data = try JSONEncoder().encode(snapshot)
        let decoded = try JSONDecoder().decode([MacProxyServiceSnapshot].self, from: data)
        XCTAssertEqual(decoded, snapshot)
    }

    func testEnableIsIdempotentAndNeverOverwritesExistingSnapshot() throws {
        let captured = [
            MacProxyServiceSnapshot(
                service: "Wi-Fi",
                web: MacProxyState(),
                secureWeb: MacProxyState(),
                socks: MacProxyState()
            )
        ]
        // No existing snapshot -> persist the freshly captured state.
        XCTAssertEqual(MacSystemProxyPlanner.snapshotToPersist(existing: nil, captured: captured), captured)
        XCTAssertEqual(MacSystemProxyPlanner.snapshotToPersist(existing: Data(), captured: captured), captured)
        // Existing snapshot present -> keep it (return nil), do not clobber the
        // genuine pre-clambhook state.
        XCTAssertNil(MacSystemProxyPlanner.snapshotToPersist(existing: Data([0x01]), captured: captured))
    }

    func testReconcileActionCoversLaunchStates() {
        XCTAssertEqual(MacSystemProxyPlanner.reconcileAction(hasSnapshot: false, desiredEnabled: true), .enable)
        XCTAssertEqual(MacSystemProxyPlanner.reconcileAction(hasSnapshot: true, desiredEnabled: true), .enable)
        XCTAssertEqual(MacSystemProxyPlanner.reconcileAction(hasSnapshot: true, desiredEnabled: false), .restore)
        XCTAssertEqual(MacSystemProxyPlanner.reconcileAction(hasSnapshot: false, desiredEnabled: false), .none)
    }

    func testRestoreCommandReappliesRealServerButDisablesEmptyState() {
        XCTAssertEqual(
            MacSystemProxyPlanner.restoreCommand(for: MacProxyState(enabled: true, server: "10.0.0.1", port: 8080)),
            .set(host: "10.0.0.1", port: 8080, enabled: true)
        )
        XCTAssertEqual(
            MacSystemProxyPlanner.restoreCommand(for: MacProxyState(enabled: false, server: "", port: 0)),
            .disable
        )
        // A server with no port cannot be re-pointed; fall back to disable.
        XCTAssertEqual(
            MacSystemProxyPlanner.restoreCommand(for: MacProxyState(enabled: true, server: "10.0.0.1", port: 0)),
            .disable
        )
    }
}
