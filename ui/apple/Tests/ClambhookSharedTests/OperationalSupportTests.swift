import XCTest
@testable import ClambhookShared

@MainActor
final class OperationalSupportTests: XCTestCase {
    func testTunnelImportDecoderAcceptsRawTOML() throws {
        let toml = """
        active = "default"

        [[profile]]
        name = "default"
        """

        XCTAssertEqual(try TunnelImportDecoder.decode(toml), toml)
    }

    func testTunnelImportDecoderAcceptsClambhookURL() throws {
        let toml = "active = \"default\"\n[[profile]]\nname = \"default\"\n"
        let encoded = Data(toml.utf8)
            .base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
        let raw = "clambhook://import?config=\(encoded)"

        XCTAssertEqual(try TunnelImportDecoder.decode(raw), toml)
    }

    func testTunnelConfigStoreDetectsPlaceholderText() {
        XCTAssertTrue(TunnelConfigStore.isPlaceholderConfigText("name = \"replace-me\""))
        XCTAssertTrue(TunnelConfigStore.isPlaceholderConfigText("password = \"replace-with-secret\""))
        XCTAssertTrue(TunnelConfigStore.isPlaceholderConfigText("address = \"proxy.example.com:443\""))

        let realConfig = """
        active = "phone"
        [[profile]]
        name = "phone"
        [[profile.chain]]
        name = "proxy"
        [[profile.chain.server]]
        name = "exit"
        address = "vpn.example.net:443"
        protocol = "shadowsocks"
        """
        XCTAssertFalse(TunnelConfigStore.isPlaceholderConfigText(realConfig))
    }

    func testDashboardDerivedDecisionsRuleHitsAndHealth() async {
        let api = FakeOperationalAPIClient()
        api.serversResult = ServersPayload(profile: "A", chains: [
            ChainPayload(name: "proxy", servers: [
                ServerPayload(name: "exit", address: "203.0.113.10:443", protocol: "trojan"),
            ]),
        ])
        api.trafficResult = TrafficSnapshotPayload(connections: [
            TrafficConnectionPayload(
                connID: "c1",
                state: "closed",
                updatedTsNs: 10,
                chainName: "proxy",
                ruleName: "ads",
                ruleAction: "block",
                target: "ads.example.com:443",
                hops: [
                    TrafficHopPayload(index: 0, name: "exit", protocol: "trojan", address: "203.0.113.10:443", state: "connected", elapsedNs: 25_000_000),
                ]
            ),
        ])
        let store = DashboardStore(api: api, snapshotStore: .inMemory)

        await store.refreshDashboard()

        XCTAssertEqual(store.recentDecisions.first?.ruleName, "ads")
        XCTAssertEqual(store.ruleHitSummaries.first?.count, 1)
        let serverID = api.serversResult.chains[0].servers[0].id
        XCTAssertEqual(store.passiveServerHealth[serverID]?.state, "healthy")
        XCTAssertEqual(store.passiveServerHealth[serverID]?.latencyNs, 25_000_000)
    }
}

private final class FakeOperationalAPIClient: ClambhookAPIProviding {
    var statusResult = StatusPayload()
    var profilesResult = ProfilesPayload()
    var serversResult = ServersPayload()
    var rulesResult = RulesPayload()
    var trafficResult = TrafficSnapshotPayload()

    func status() async throws -> StatusPayload { statusResult }
    func profiles() async throws -> ProfilesPayload { profilesResult }
    func servers() async throws -> ServersPayload { serversResult }
    func rules() async throws -> RulesPayload { rulesResult }
    func traffic() async throws -> TrafficSnapshotPayload { trafficResult }
    func connect() async throws {}
    func disconnect() async throws {}
    func setActiveProfile(_ name: String) async throws {}
}
