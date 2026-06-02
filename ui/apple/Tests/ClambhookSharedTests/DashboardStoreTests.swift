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

    func testRefreshUsesSingleDashboardSnapshotWhenAvailable() async throws {
        let snapshotURL = temporaryURL("tunnel-dashboard-snapshot.json")
        let snapshotStore = FileSnapshotStore(fileURL: snapshotURL)
        let api = FakeDashboardAPI()
        api.dashboardResult = TunnelDashboardPayload(
            status: StatusPayload(
                running: true,
                profile: "phone",
                listeners: [ListenerStatusPayload(protocol: "tun", addr: "packet", activeConns: 2)]
            ),
            profiles: ProfilesPayload(profiles: ["phone", "backup"], active: "phone"),
            servers: ServersPayload(profile: "phone", chains: [
                ChainPayload(name: "proxy", servers: [
                    ServerPayload(name: "exit", address: "example.invalid:443", protocol: "shadowsocks")
                ])
            ]),
            rules: RulesPayload(profile: "phone", rules: [
                RulePayload(name: "ads", action: "block", domainSuffixes: ["ads.example.com"])
            ]),
            traffic: TrafficSnapshotPayload(
                summary: TrafficSummaryPayload(activeConnections: 2, rxBps: 4096, txBps: 1024),
                connections: [TrafficConnectionPayload(connID: "c1", state: "active", target: "example.com:443")]
            )
        )
        let store = DashboardStore(api: api, snapshotStore: snapshotStore)

        await store.refreshDashboard()

        XCTAssertEqual(api.dashboardCalls, 1)
        XCTAssertEqual(api.statusCalls, 0)
        XCTAssertEqual(api.profilesCalls, 0)
        XCTAssertEqual(api.serversCalls, 0)
        XCTAssertEqual(api.rulesCalls, 0)
        XCTAssertEqual(api.trafficCalls, 0)
        XCTAssertEqual(store.status.profile, "phone")
        XCTAssertEqual(store.profiles.profiles, ["phone", "backup"])
        XCTAssertEqual(store.servers.chains.first?.name, "proxy")
        XCTAssertEqual(store.rules.rules.first?.name, "ads")
        XCTAssertEqual(store.traffic.summary.rxBps, 4096)
        let snapshot = try await snapshotStore.load()
        XCTAssertTrue(snapshot.apiOnline)
        XCTAssertEqual(snapshot.profile, "phone")
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
        XCTAssertEqual(defaultAppGroupIdentifier, "group.org.jpfchang.clambhook")
        XCTAssertEqual(defaultPrivacyPolicyURL.absoluteString, "https://jpfchang.org/clambhook/privacy")
        XCTAssertEqual(defaultSupportURL.absoluteString, "https://jpfchang.org/clambhook/support")
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

private class FakeAPIClient: ClambhookAPIProviding {
    var statusResult = StatusPayload(running: false, profile: "", listeners: [])
    var profilesResult = ProfilesPayload(profiles: [], active: "")
    var serversResult = ServersPayload(profile: "", chains: [])
    var rulesResult = RulesPayload(profile: "", rules: [])
    var trafficResult = TrafficSnapshotPayload()
    var ruleTestResult = RuleTestResponse()
    private(set) var connectCalls = 0
    private(set) var disconnectCalls = 0
    private(set) var selectedProfiles: [String] = []
    private(set) var statusCalls = 0
    private(set) var profilesCalls = 0
    private(set) var serversCalls = 0
    private(set) var rulesCalls = 0
    private(set) var trafficCalls = 0

    func status() async throws -> StatusPayload {
        statusCalls += 1
        return statusResult
    }

    func profiles() async throws -> ProfilesPayload {
        profilesCalls += 1
        return profilesResult
    }

    func servers() async throws -> ServersPayload {
        serversCalls += 1
        return serversResult
    }

    func rules() async throws -> RulesPayload {
        rulesCalls += 1
        return rulesResult
    }

    func testRule(network: String, target: String, profile: String) async throws -> RuleTestResponse {
        ruleTestResult
    }

    func traffic() async throws -> TrafficSnapshotPayload {
        trafficCalls += 1
        return trafficResult
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

private final class FakeDashboardAPI: FakeAPIClient, ClambhookDashboardProviding {
    var dashboardResult = TunnelDashboardPayload()
    private(set) var dashboardCalls = 0

    func dashboard() async throws -> TunnelDashboardPayload {
        dashboardCalls += 1
        return dashboardResult
    }
}
