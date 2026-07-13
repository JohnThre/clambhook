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
            ], generatedRules: [
                RulePayload(name: "subscription:ads:domains", action: "block", domainSuffixes: ["tracker.example.com"])
            ], effectiveRules: [
                RulePayload(name: "ads", action: "block", domainSuffixes: ["ads.example.com"]),
                RulePayload(name: "subscription:ads:domains", action: "block", domainSuffixes: ["tracker.example.com"])
            ]),
            policyGroups: PolicyGroupsPayload(profile: "phone", groups: [
                PolicyGroupPayload(name: "auto", type: "url-test", chains: ["proxy"], selectedChain: "proxy")
            ]),
            ruleSubscriptions: RuleSubscriptionsPayload(profile: "phone", subscriptions: [
                RuleSubscriptionPayload(
                    name: "ads",
                    url: "https://lists.example.invalid/ads.txt",
                    format: "auto",
                    action: "block",
                    cached: true,
                    domainCount: 1,
                    generatedRules: ["subscription:ads:domains"]
                )
            ]),
            traffic: TrafficSnapshotPayload(
                summary: TrafficSummaryPayload(activeConnections: 2, rxBps: 4096, txBps: 1024),
                connections: [TrafficConnectionPayload(connID: "c1", state: "active", target: "example.com:443")]
            ),
            dns: DNSPayload(
                profile: "phone",
                strategy: "encrypted",
                enabled: true,
                timeout: "5s",
                upstreams: [DNSUpstreamPayload(name: "cf", protocol: "doh", url: "https://cloudflare-dns.com/dns-query")],
                interceptsPort53: true,
                upstreamRoutes: [DNSUpstreamRoutePayload(name: "cf", protocol: "doh", target: "cloudflare-dns.com:443", network: "tcp", action: "chain", chainName: "proxy")]
            ),
            networkSettings: TunnelNetworkSettingsPayload(
                mtu: 1400,
                dnsServers: ["198.18.0.1"],
                includedRoutes: ["0.0.0.0/0"],
                excludedRoutes: ["127.0.0.0/8"]
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
        XCTAssertEqual(store.rules.generatedRules.first?.name, "subscription:ads:domains")
        XCTAssertEqual(store.rules.effectiveRules.count, 2)
        XCTAssertEqual(store.policyGroups.groups.first?.selectedChain, "proxy")
        XCTAssertEqual(store.ruleSubscriptions.subscriptions.first?.generatedRules, ["subscription:ads:domains"])
        XCTAssertEqual(store.traffic.summary.rxBps, 4096)
        XCTAssertEqual(store.dns.strategy, "encrypted")
        XCTAssertEqual(store.dns.upstreams.first?.name, "cf")
        XCTAssertEqual(store.dns.upstreamRoutes.first?.chainName, "proxy")
        XCTAssertEqual(store.networkSettings.dnsServers, ["198.18.0.1"])
        XCTAssertEqual(store.networkSettings.includedRoutes, ["0.0.0.0/0"])
        let snapshot = try await snapshotStore.load()
        XCTAssertTrue(snapshot.apiOnline)
        XCTAssertEqual(snapshot.profile, "phone")
        XCTAssertEqual(snapshot.listenerCount, 1)
    }

    func testDashboardPayloadDecodesMissingPolicyGroups() throws {
        let data = Data("""
        {
          "status": {"running": true, "profile": "A", "listeners": []},
          "profiles": {"profiles": ["A"], "active": "A"},
          "servers": {"profile": "A", "chains": []},
          "rules": {"profile": "A", "rules": []},
          "traffic": {"summary": {"active_connections": 0}, "connections": []}
        }
        """.utf8)

        let payload = try JSONDecoder().decode(TunnelDashboardPayload.self, from: data)

        XCTAssertEqual(payload.policyGroups, PolicyGroupsPayload())
        XCTAssertEqual(payload.ruleSubscriptions, RuleSubscriptionsPayload())
        XCTAssertEqual(payload.dns, DNSPayload())
        XCTAssertEqual(payload.networkSettings, TunnelNetworkSettingsPayload())
        XCTAssertEqual(payload.status.profile, "A")
    }

    func testDashboardPayloadDecodesDNSWhenPresent() throws {
        let data = Data("""
        {
          "status": {"running": true, "profile": "A", "listeners": []},
          "profiles": {"profiles": ["A"], "active": "A"},
          "servers": {"profile": "A", "chains": []},
          "rules": {"profile": "A", "rules": []},
          "traffic": {"summary": {"active_connections": 0}, "connections": []},
          "dns": {
            "profile": "A",
            "strategy": "encrypted",
            "enabled": true,
            "timeout": "5s",
            "intercepts_port_53": true,
            "upstreams": [{"name": "cf", "protocol": "doh", "url": "https://cloudflare-dns.com/dns-query"}],
            "upstream_routes": [{"name": "cf", "protocol": "doh", "target": "cloudflare-dns.com:443", "network": "tcp", "action": "chain", "chain_name": "proxy"}]
          }
        }
        """.utf8)

        let payload = try JSONDecoder().decode(TunnelDashboardPayload.self, from: data)

        XCTAssertTrue(payload.dns.enabled)
        XCTAssertEqual(payload.dns.strategy, "encrypted")
        XCTAssertEqual(payload.dns.upstreams.first?.targetDescription, "https://cloudflare-dns.com/dns-query")
        XCTAssertEqual(payload.dns.upstreamRoutes.first?.chainName, "proxy")
    }

    func testDashboardPayloadDecodesNetworkSettingsWhenPresent() throws {
        let data = Data("""
        {
          "status": {"running": true, "profile": "A", "listeners": []},
          "profiles": {"profiles": ["A"], "active": "A"},
          "servers": {"profile": "A", "chains": []},
          "rules": {"profile": "A", "rules": []},
          "traffic": {"summary": {"active_connections": 0}, "connections": []},
          "network_settings": {
            "mtu": 1400,
            "remote_address": "example.invalid",
            "ipv4": [{"address": "198.18.0.1", "prefix_len": 30}],
            "ipv6": [],
            "dns_servers": ["198.18.0.1"],
            "included_routes": ["0.0.0.0/0"],
            "excluded_routes": ["127.0.0.0/8"],
            "http_proxy": {"host": "127.0.0.1", "port": 18080}
          }
        }
        """.utf8)

        let payload = try JSONDecoder().decode(TunnelDashboardPayload.self, from: data)

        XCTAssertEqual(payload.networkSettings.mtu, 1400)
        XCTAssertEqual(payload.networkSettings.remoteAddress, "example.invalid")
        XCTAssertEqual(payload.networkSettings.ipv4.first?.address, "198.18.0.1")
        XCTAssertEqual(payload.networkSettings.dnsServers, ["198.18.0.1"])
        XCTAssertEqual(payload.networkSettings.includedRoutes, ["0.0.0.0/0"])
        XCTAssertEqual(payload.networkSettings.excludedRoutes, ["127.0.0.0/8"])
        XCTAssertEqual(payload.networkSettings.httpProxy, TunnelProxyPayload(host: "127.0.0.1", port: 18080))
    }

    func testTunnelPolicyGroupSelectionEditorUpdatesManualGroup() throws {
        let url = temporaryURL("policy-selection.toml")
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try """
        active = "A"

        [[profile]]
        name = "A"

          [[profile.policy_group]]
          name = "manual"
          type = "select"
          chains = ["proxy", "backup"]
          selected = "proxy"
        """.write(to: url, atomically: true, encoding: .utf8)

        try updateTunnelPolicyGroupSelection(
            configPath: url.path,
            profileName: "A",
            groupName: "manual",
            chainName: "backup"
        )

        let text = try String(contentsOf: url, encoding: .utf8)
        XCTAssertTrue(text.contains(#"selected = "backup""#))
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
        XCTAssertEqual(defaultPrivacyPolicyURL.absoluteString, "https://store.clambercloud.com/clambhook/privacy")
        XCTAssertEqual(defaultSupportURL.absoluteString, "https://store.clambercloud.com/clambhook/support")
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
    var policyGroupsResult = PolicyGroupsPayload()
    var rulesResult = RulesPayload(profile: "", rules: [])
    var trafficResult = TrafficSnapshotPayload()
    var dnsResult = DNSPayload()
    var ruleTestResult = RuleTestResponse()
    private(set) var connectCalls = 0
    private(set) var disconnectCalls = 0
    private(set) var selectedProfiles: [String] = []
    private(set) var selectedPolicyGroups: [(profile: String, group: String, chain: String)] = []
    private(set) var statusCalls = 0
    private(set) var profilesCalls = 0
    private(set) var serversCalls = 0
    private(set) var policyGroupCalls = 0
    private(set) var rulesCalls = 0
    private(set) var trafficCalls = 0
    private(set) var dnsCalls = 0

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

    func policyGroups() async throws -> PolicyGroupsPayload {
        policyGroupCalls += 1
        return policyGroupsResult
    }

    func rules() async throws -> RulesPayload {
        rulesCalls += 1
        return rulesResult
    }

    func dns() async throws -> DNSPayload {
        dnsCalls += 1
        return dnsResult
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

    func selectPolicyGroup(profile: String, group: String, chain: String) async throws -> PolicyGroupsPayload {
        selectedPolicyGroups.append((profile, group, chain))
        return policyGroupsResult
    }

    func testPolicyGroup(group: String, profile: String) async throws -> PolicyGroupsPayload {
        return policyGroupsResult
    }

    func updateDNS(_ request: DNSUpdateRequest, profile: String) async throws -> DNSPayload {
        return DNSPayload()
    }

    func exportConfig() async throws -> String { return "" }

    func importConfig(_ toml: String) async throws -> ConfigImportResponse {
        return ConfigImportResponse()
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
