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

    func testProfileTemplateOrderKeepsAdvancedLast() {
        XCTAssertEqual(
            TunnelProfileTemplate.allCases.map(\.rawValue),
            ["shadowsocks", "wireguard", "openvpn", "trojan", "tor", "clambback", "advanced"]
        )
    }

    func testShadowsocksTemplateBuildsTypedSettingsRequest() throws {
        let draft = TunnelProfileCreateDraft(
            serverAddress: "example.com:8388",
            shadowsocks: TunnelShadowsocksTemplateSettings(password: "secret")
        )

        let request = try XCTUnwrap(draft.makeCreateRequest())

        XCTAssertTrue(draft.isInputComplete)
        XCTAssertEqual(request.protocol, "shadowsocks")
        XCTAssertEqual(request.serverAddress, "example.com:8388")
        XCTAssertEqual(request.settingsTOML, "")
        XCTAssertEqual(request.settings?["method"], .string("chacha20-ietf-poly1305"))
        XCTAssertEqual(request.settings?["password"], .string("secret"))
    }

    func testWireGuardTemplateBuildsNestedSettingsRequest() throws {
        let draft = TunnelProfileCreateDraft(
            template: .wireguard,
            serverAddress: "1.2.3.4:51820",
            wireguard: TunnelWireGuardTemplateSettings(
                privateKey: "private",
                interfaceAddresses: "10.0.0.2/32\n10.0.0.3/32",
                dnsServers: "1.1.1.1, 8.8.8.8",
                peerPublicKey: "public",
                presharedKey: "psk",
                allowedIPs: "0.0.0.0/0, ::/0",
                persistentKeepalive: 25,
                mtu: 1280,
                logLevel: "verbose"
            )
        )

        let request = try XCTUnwrap(draft.makeCreateRequest())
        let data = try JSONEncoder().encode(request)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let settings = try XCTUnwrap(object["settings"] as? [String: Any])
        let peers = try XCTUnwrap(settings["peers"] as? [[String: Any]])
        let peer = try XCTUnwrap(peers.first)

        XCTAssertTrue(draft.isInputComplete)
        XCTAssertEqual(object["protocol"] as? String, "wireguard")
        XCTAssertEqual(settings["private_key"] as? String, "private")
        XCTAssertEqual(settings["addresses"] as? [String], ["10.0.0.2/32", "10.0.0.3/32"])
        XCTAssertEqual(settings["dns"] as? [String], ["1.1.1.1", "8.8.8.8"])
        XCTAssertEqual(settings["mtu"] as? Int, 1280)
        XCTAssertEqual(settings["log_level"] as? String, "verbose")
        XCTAssertEqual(peer["public_key"] as? String, "public")
        XCTAssertEqual(peer["endpoint"] as? String, "1.2.3.4:51820")
        XCTAssertEqual(peer["allowed_ips"] as? [String], ["0.0.0.0/0", "::/0"])
        XCTAssertEqual(peer["preshared_key"] as? String, "psk")
        XCTAssertEqual(peer["persistent_keepalive"] as? Int, 25)
    }

    func testAdvancedTemplateDoesNotBuildSingleProfileRequest() {
        let draft = TunnelProfileCreateDraft(template: .advanced, advancedTOML: "active = \"default\"")

        XCTAssertTrue(draft.isInputComplete)
        XCTAssertNil(draft.makeCreateRequest())
    }

    func testTunnelRecoveryClassifierRecognizesPrimaryFailures() {
        XCTAssertEqual(
            TunnelRecoveryClassifier.issue(forRawError: "user denied VPN permission").kind,
            .vpnPermissionDenied
        )
        XCTAssertEqual(
            TunnelRecoveryClassifier.issue(forRawError: "profile default chain proxy server 0 protocol tor: chain proxy: protocol tor does not support UDP").kind,
            .noUDPSupport
        )
        XCTAssertEqual(
            TunnelRecoveryClassifier.issue(forRawError: "openvpn: server rejected auth: AUTH_FAILED").kind,
            .badServerCredentials
        )
        XCTAssertEqual(
            TunnelRecoveryClassifier.issue(forRawError: "configuration invalid").kind,
            .invalidEntitlementOrProfile
        )
    }

    func testReviewedTunnelImportRequestWireShape() throws {
        let request = ReviewedTunnelImportRequest(
            importText: "active = \"phone\"",
            profiles: [
                ReviewedTunnelImportProfile(sourceName: "phone", targetName: "phone-sg")
            ],
            activateProfile: "phone-sg"
        )

        let data = try JSONEncoder().encode(request)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let profiles = try XCTUnwrap(object["profiles"] as? [[String: Any]])

        XCTAssertEqual(object["import_text"] as? String, "active = \"phone\"")
        XCTAssertEqual(object["activate_profile"] as? String, "phone-sg")
        XCTAssertEqual(profiles.first?["source_name"] as? String, "phone")
        XCTAssertEqual(profiles.first?["target_name"] as? String, "phone-sg")
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
            TrafficConnectionPayload(
                connID: "c2",
                state: "closed",
                updatedTsNs: 11,
                chainName: "proxy",
                target: "default.example.com:443"
            ),
        ])
        let store = DashboardStore(api: api, snapshotStore: .inMemory)

        await store.refreshDashboard()

        XCTAssertEqual(store.recentDecisions.first?.target, "default.example.com:443")
        XCTAssertEqual(store.recentDecisions.dropFirst().first?.ruleName, "ads")
        XCTAssertEqual(store.ruleHitSummaries.first?.count, 1)
        let serverID = api.serversResult.chains[0].servers[0].id
        XCTAssertEqual(store.passiveServerHealth[serverID]?.state, "healthy")
        XCTAssertEqual(store.passiveServerHealth[serverID]?.latencyNs, 25_000_000)
    }

    func testPolicySelectorSummaryUsesStaticDefaultRoute() {
        let summary = PolicySelectorSummary.build(
            policyGroups: PolicyGroupsPayload(),
            servers: ServersPayload(profile: "A", chains: [ChainPayload(name: "proxy", servers: [])]),
            traffic: TrafficSnapshotPayload(connections: [
                TrafficConnectionPayload(connID: "c1", ruleAction: "direct"),
                TrafficConnectionPayload(connID: "c2", ruleAction: "block"),
                TrafficConnectionPayload(connID: "c3", chainName: "proxy"),
            ])
        )

        XCTAssertEqual(summary.proxyCount, 1)
        XCTAssertEqual(summary.directCount, 1)
        XCTAssertEqual(summary.blockCount, 1)
        XCTAssertEqual(summary.routes.count, 1)
        XCTAssertEqual(summary.routes.first?.selectedChain, "proxy")
        XCTAssertEqual(summary.routes.first?.healthState, .staticRoute)
    }

    func testPolicySelectorSummaryReportsHealthySelectedChain() {
        let summary = PolicySelectorSummary.build(
            policyGroups: PolicyGroupsPayload(profile: "A", groups: [
                PolicyGroupPayload(name: "auto", chains: ["proxy", "backup"], selectedChain: "proxy", results: [
                    PolicyProbeResultPayload(chainName: "proxy", healthy: true, latencyNs: 20),
                    PolicyProbeResultPayload(chainName: "backup", healthy: false, error: "timeout"),
                ]),
            ]),
            servers: ServersPayload(profile: "A"),
            traffic: TrafficSnapshotPayload()
        )

        XCTAssertEqual(summary.routes.count, 1)
        XCTAssertEqual(summary.routes.first?.groupName, "auto")
        XCTAssertEqual(summary.routes.first?.selectedChain, "proxy")
        XCTAssertEqual(summary.routes.first?.healthState, .healthy)
        XCTAssertEqual(summary.routes.first?.healthText, "Healthy / 1/2")
    }

    func testPolicySelectorSummaryReportsFallbackWhenSelectedChainUnhealthy() {
        let summary = PolicySelectorSummary.build(
            policyGroups: PolicyGroupsPayload(profile: "A", groups: [
                PolicyGroupPayload(name: "auto", chains: ["proxy", "backup"], selectedChain: "proxy", results: [
                    PolicyProbeResultPayload(chainName: "proxy", healthy: false, error: "timeout"),
                    PolicyProbeResultPayload(chainName: "backup", healthy: true, latencyNs: 30),
                ]),
            ]),
            servers: ServersPayload(profile: "A"),
            traffic: TrafficSnapshotPayload()
        )

        XCTAssertEqual(summary.routes.count, 1)
        XCTAssertEqual(summary.routes.first?.selectedChain, "proxy")
        XCTAssertEqual(summary.routes.first?.healthState, .fallback)
        XCTAssertEqual(summary.routes.first?.healthText, "Fallback / 1/2 healthy")
    }

    func testPolicySelectorSummaryKeepsTopThreeRuleHits() {
        let summary = PolicySelectorSummary.build(
            policyGroups: PolicyGroupsPayload(),
            servers: ServersPayload(profile: "A"),
            traffic: TrafficSnapshotPayload(ruleHits: [
                TrafficRuleHitPayload(ruleName: "one", action: "direct", count: 1),
                TrafficRuleHitPayload(ruleName: "four", action: "block", count: 4),
                TrafficRuleHitPayload(ruleName: "three", action: "chain:proxy", count: 3),
                TrafficRuleHitPayload(ruleName: "two", action: "reject", count: 2),
            ])
        )

        XCTAssertEqual(summary.topRuleHits.map(\.ruleName), ["four", "three", "two"])
    }

    func testDashboardRecoveryIssueUsesRecentClassifiedHopError() async {
        let api = FakeOperationalAPIClient()
        api.trafficResult = TrafficSnapshotPayload(connections: [
            TrafficConnectionPayload(
                connID: "c1",
                state: "closed",
                updatedTsNs: 10,
                hops: [
                    TrafficHopPayload(index: 0, state: "error", error: "openvpn: server rejected auth: AUTH_FAILED"),
                ]
            ),
        ])
        let store = DashboardStore(api: api, snapshotStore: .inMemory)

        await store.refreshDashboard()

        XCTAssertEqual(store.recoveryIssue?.kind, .badServerCredentials)
        XCTAssertEqual(store.errorText, store.recoveryIssue?.message)
    }
}

private final class FakeOperationalAPIClient: ClambhookAPIProviding {
    var statusResult = StatusPayload()
    var profilesResult = ProfilesPayload()
    var serversResult = ServersPayload()
    var policyGroupsResult = PolicyGroupsPayload()
    var rulesResult = RulesPayload()
    var dnsResult = DNSPayload()
    var trafficResult = TrafficSnapshotPayload()
    var ruleTestResult = RuleTestResponse()

    func status() async throws -> StatusPayload { statusResult }
    func profiles() async throws -> ProfilesPayload { profilesResult }
    func servers() async throws -> ServersPayload { serversResult }
    func policyGroups() async throws -> PolicyGroupsPayload { policyGroupsResult }
    func rules() async throws -> RulesPayload { rulesResult }
    func dns() async throws -> DNSPayload { dnsResult }
    func testRule(network: String, target: String, profile: String) async throws -> RuleTestResponse { ruleTestResult }
    func traffic() async throws -> TrafficSnapshotPayload { trafficResult }
    func connect() async throws {}
    func disconnect() async throws {}
    func setActiveProfile(_ name: String) async throws {}
    func selectPolicyGroup(profile: String, group: String, chain: String) async throws -> PolicyGroupsPayload { policyGroupsResult }
}
