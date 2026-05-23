import XCTest
@testable import ClambhookShared

@MainActor
final class DashboardStoreTests: XCTestCase {
    func testRefreshLoadsDashboardAndPersistsWidgetSnapshot() async throws {
        let snapshotURL = temporaryURL("dashboard-snapshot.json")
        let snapshotStore = FileSnapshotStore(fileURL: snapshotURL)
        let api = FakeAPIClient()
        api.statusResult = StatusPayload(
            running: true,
            profile: "A",
            listeners: [ListenerStatusPayload(protocol: "socks5", addr: "127.0.0.1:1080", activeConns: 3)]
        )
        api.profilesResult = ProfilesPayload(profiles: ["A", "B"], active: "A")
        api.trafficResult = TrafficSnapshotPayload(
            summary: TrafficSummaryPayload(activeConnections: 1, rxBps: 2048, txBps: 1024),
            connections: [TrafficConnectionPayload(connID: "c1", state: "active", target: "example.com:443")]
        )
        api.serversResult = ServersPayload(
            profile: "A",
            chains: [ChainPayload(name: "default", servers: [
                ServerPayload(
                    name: "london",
                    address: "81.2.69.142:443",
                    protocol: "trojan",
                    geo: LocationPayload(country: "United Kingdom", countryCode: "GB", city: "London", latitude: 0, longitude: 0),
                    geoError: nil
                )
            ])]
        )
        let store = DashboardStore(api: api, snapshotStore: snapshotStore)

        await store.refreshDashboard()

        XCTAssertTrue(store.status.running)
        XCTAssertEqual(store.profiles.profiles, ["A", "B"])
        XCTAssertEqual(store.servers.chains.first?.servers.first?.name, "london")
        XCTAssertEqual(store.traffic.connections.first?.target, "example.com:443")
        let snapshot = try await snapshotStore.load()
        XCTAssertTrue(snapshot.apiOnline)
        XCTAssertTrue(snapshot.running)
        XCTAssertEqual(snapshot.profile, "A")
        XCTAssertEqual(snapshot.listenerCount, 1)
    }

    func testApplyConnectionBytesKeepsLatestSamplesAndSnapshotRates() async throws {
        let snapshotURL = temporaryURL("bandwidth-snapshot.json")
        let snapshotStore = FileSnapshotStore(fileURL: snapshotURL)
        let store = DashboardStore(api: FakeAPIClient(), snapshotStore: snapshotStore)

        for index in 0..<65 {
            await store.apply(event: DaemonEvent(
                shardID: 1,
                lamport: UInt64(index + 1),
                tsNs: 0,
                type: "connection.bytes",
                data: [
                    "rx_delta": Double(index + 1) * 1024,
                    "tx_delta": Double(index + 1) * 512,
                    "interval_ns": Double(1_000_000_000),
                ]
            ))
        }

        XCTAssertEqual(store.bandwidthSamples.count, 60)
        XCTAssertEqual(store.bandwidthSamples.first?.rxBps, 6 * 1024)
        XCTAssertEqual(store.currentBandwidth.rxBps, 65 * 1024)
        let snapshot = try await snapshotStore.load()
        XCTAssertEqual(snapshot.rxBps, 65 * 1024)
        XCTAssertEqual(snapshot.txBps, 65 * 512)
    }

    func testApplyLogLineCapsRecentLogs() async {
        let store = DashboardStore(api: FakeAPIClient(), snapshotStore: .inMemory)

        for index in 0..<205 {
            await store.apply(event: DaemonEvent(
                shardID: 0,
                lamport: UInt64(index + 1),
                tsNs: 0,
                type: "log.line",
                data: ["line": "line-\(index)"]
            ))
        }

        XCTAssertEqual(store.logs.count, 200)
        XCTAssertEqual(store.logs.first, "line-5")
        XCTAssertEqual(store.logs.last, "line-204")
    }

    func testApplyLogLineUsesConfiguredRetention() async {
        let store = DashboardStore(api: FakeAPIClient(), snapshotStore: .inMemory, logRetention: 50)

        for index in 0..<55 {
            await store.apply(event: DaemonEvent(
                shardID: 0,
                lamport: UInt64(index + 1),
                tsNs: 0,
                type: "log.line",
                data: ["line": "line-\(index)"]
            ))
        }

        XCTAssertEqual(store.logs.count, 50)
        XCTAssertEqual(store.logs.first, "line-5")
        XCTAssertEqual(store.logs.last, "line-54")
    }

    func testSettingsNormalizationClampsValuesAndRejectsUnsupportedEndpoints() {
        let settings = AppSettings(
            apiEndpoint: URL(string: "ftp://example.test")!,
            daemonBinaryPath: " /tmp/clambhook \n",
            daemonConfigPath: "\t/tmp/config.toml ",
            refreshIntervalSeconds: 99,
            logRetention: 5,
            appGroupIdentifier: " "
        ).normalized()

        XCTAssertEqual(settings.apiEndpoint, defaultAPIEndpoint)
        XCTAssertEqual(settings.daemonBinaryPath, "/tmp/clambhook")
        XCTAssertEqual(settings.daemonConfigPath, "/tmp/config.toml")
        XCTAssertEqual(settings.refreshIntervalSeconds, maxRefreshIntervalSeconds)
        XCTAssertEqual(settings.logRetention, minLogRetention)
        XCTAssertEqual(settings.appGroupIdentifier, defaultAppGroupIdentifier)
    }

    func testCountryFlagAndRateFormatting() {
        XCTAssertEqual(countryFlag("GB"), "🇬🇧")
        XCTAssertEqual(countryFlag(""), "--")
        XCTAssertEqual(formatRate(500), "500 B/s")
        XCTAssertEqual(formatRate(1536), "1.5 KB/s")
        XCTAssertEqual(formatRate(2 * 1024 * 1024), "2.0 MB/s")
    }
}

private func temporaryURL(_ name: String) -> URL {
    FileManager.default.temporaryDirectory
        .appendingPathComponent(UUID().uuidString, isDirectory: true)
        .appendingPathComponent(name)
}

private final class FakeAPIClient: ClambhookAPIProviding {
    var statusResult = StatusPayload(running: false, profile: "", listeners: [])
    var profilesResult = ProfilesPayload(profiles: [], active: "")
    var serversResult = ServersPayload(profile: "", chains: [])
    var trafficResult = TrafficSnapshotPayload()
    private(set) var connectCalls = 0
    private(set) var disconnectCalls = 0
    private(set) var selectedProfiles: [String] = []

    func status() async throws -> StatusPayload {
        statusResult
    }

    func profiles() async throws -> ProfilesPayload {
        profilesResult
    }

    func servers() async throws -> ServersPayload {
        serversResult
    }

    func traffic() async throws -> TrafficSnapshotPayload {
        trafficResult
    }

    func connect() async throws {
        connectCalls += 1
    }

    func disconnect() async throws {
        disconnectCalls += 1
    }

    func setActiveProfile(_ name: String) async throws {
        selectedProfiles.append(name)
    }
}
