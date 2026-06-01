import XCTest
@testable import ClambhookShared

@MainActor
final class InspectionSupportTests: XCTestCase {
    func testInspectionConnectionsFiltersAndPinsFirst() {
        let traffic = TrafficSnapshotPayload(connections: [
            TrafficConnectionPayload(connID: "a", state: "closed", ruleAction: "direct", target: "alpha.example:443"),
            TrafficConnectionPayload(connID: "b", state: "active", ruleAction: "block", target: "beta.example:443"),
            TrafficConnectionPayload(connID: "c", state: "active", chainName: "proxy", target: "gamma.example:443"),
        ])

        XCTAssertEqual(
            traffic.inspectionConnections(filter: InspectionFilterKind.all, pinnedIDs: ["c"]).map(\.connID),
            ["c", "a", "b"]
        )
        XCTAssertEqual(
            traffic.inspectionConnections(filter: InspectionFilterKind.active, pinnedIDs: ["c"]).map(\.connID),
            ["c", "b"]
        )
        XCTAssertEqual(
            traffic.inspectionConnections(filter: InspectionFilterKind.pinned, pinnedIDs: ["c"]).map(\.connID),
            ["c"]
        )
        XCTAssertEqual(
            traffic.inspectionConnections(filter: InspectionFilterKind.block, query: "beta", pinnedIDs: ["c"]).map(\.connID),
            ["b"]
        )
    }

    func testRedactedExportKeepsTargetButRedactsAddressesPathsAndSecrets() {
        let traffic = TrafficSnapshotPayload(
            summary: TrafficSummaryPayload(
                activeConnections: 1,
                rxBps: 10,
                txBps: 5,
                rxTotal: 100,
                txTotal: 50,
                historyLimit: 500,
                historyPath: "/Users/me/Library/Caches/clambhook/traffic-history.json",
                historyPersisted: true,
                persistError: "read /Users/me/private/config.toml token=abc"
            ),
            connections: []
        )
        let connection = TrafficConnectionPayload(
            connID: "c1",
            state: "closed",
            listener: TrafficListenerPayload(protocol: "http", addr: "127.0.0.1:8080"),
            clientAddr: "10.0.0.2:54321",
            target: "example.com:443",
            targetHost: "example.com",
            hops: [
                TrafficHopPayload(index: 0, name: "exit", protocol: "trojan", address: "203.0.113.10:443", state: "connected"),
            ],
            timeline: [
                TrafficTimelinePayload(tsNs: 1, type: "connection.opened", title: "Opened", detail: "http 10.0.0.2:54321 bearer secret-token"),
            ],
            visibility: TrafficVisibilityPayload(kind: "http_connect", method: "CONNECT", host: "example.com", port: "443")
        )

        let payload = InspectionExportBuilder.payload(
            scope: "test",
            traffic: traffic,
            connections: [connection],
            logs: ["loaded /Users/me/private/config.toml password=secret", "dial 203.0.113.10:443"]
        )
        let json = InspectionExportBuilder.jsonString(
            scope: "test",
            traffic: traffic,
            connections: [connection],
            logs: ["loaded /Users/me/private/config.toml password=secret", "dial 203.0.113.10:443"],
            generatedAt: Date(timeIntervalSince1970: 0)
        )

        XCTAssertEqual(payload.summary.historyPath, "[redacted-path]")
        XCTAssertEqual(payload.connections.first?.listener.addr, "[redacted-address]")
        XCTAssertEqual(payload.connections.first?.clientAddr, "[redacted-address]")
        XCTAssertEqual(payload.connections.first?.hops.first?.address, "[redacted-address]")
        XCTAssertTrue(json.contains("example.com:443"))
        XCTAssertFalse(json.contains("127.0.0.1"))
        XCTAssertFalse(json.contains("10.0.0.2"))
        XCTAssertFalse(json.contains("203.0.113.10"))
        XCTAssertFalse(json.contains("secret-token"))
        XCTAssertFalse(json.contains("password=secret"))
        XCTAssertFalse(json.contains("/Users/me/private/config.toml"))
    }

    func testSettingsDecodeDefaultsInspectionFieldsAndNormalizePins() throws {
        let data = Data("""
        {
          "apiEndpoint": "http://127.0.0.1:9090",
          "refreshIntervalSeconds": 2,
          "logRetention": 200
        }
        """.utf8)

        let decoded = try JSONDecoder().decode(AppSettings.self, from: data).normalized()

        XCTAssertFalse(decoded.inspectionLockEnabled)
        XCTAssertEqual(decoded.pinnedConnectionIDs, [])

        let normalized = AppSettings(pinnedConnectionIDs: [" c2 ", "", "c1", "c2"]).normalized()
        XCTAssertEqual(normalized.pinnedConnectionIDs, ["c1", "c2"])
    }

    func testInspectionLockStateAuthenticatesWithInjectedAuthenticator() async {
        let authenticator = MockBiometricAuthenticator()
        let state = InspectionLockState(authenticator: authenticator)

        state.lockIfNeeded(enabled: true)
        XCTAssertTrue(state.isLocked)

        await state.authenticateIfNeeded(enabled: true)

        XCTAssertEqual(authenticator.authenticateCalls, 1)
        XCTAssertFalse(state.isLocked)
        XCTAssertEqual(state.message, "")
    }

    func testInspectionLockStateClearsWhenBiometricsUnavailable() {
        let authenticator = MockBiometricAuthenticator(status: BiometricAuthStatus(
            isAvailable: false,
            label: "Face ID",
            reason: "not enrolled"
        ))
        let state = InspectionLockState(authenticator: authenticator)

        state.lockIfNeeded(enabled: true)

        XCTAssertFalse(state.isLocked)
        XCTAssertEqual(state.message, "not enrolled")
    }
}

private final class MockBiometricAuthenticator: BiometricAuthenticating {
    var currentStatus: BiometricAuthStatus
    var authenticateCalls = 0
    var authenticateError: Error?

    init(status: BiometricAuthStatus = BiometricAuthStatus(isAvailable: true, label: "Face ID")) {
        self.currentStatus = status
    }

    func status() -> BiometricAuthStatus {
        currentStatus
    }

    func authenticate(reason: String) async throws {
        authenticateCalls += 1
        if let authenticateError {
            throw authenticateError
        }
    }
}
