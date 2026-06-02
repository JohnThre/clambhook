import XCTest
@testable import ClambhookShared

final class RuleTesterTests: XCTestCase {
    func testRuleTesterMatchesSuffixAndReturnsChainDetails() throws {
        let response = try RuleTester.test(
            network: "tcp",
            target: "cdn.ads.example.com:443",
            profile: "A",
            rules: [
                RulePayload(name: "ads", action: "chain:proxy", domainSuffixes: ["ads.example.com"], networks: ["tcp"])
            ],
            chains: [
                ChainPayload(
                    name: "proxy",
                    hopCount: 1,
                    capabilities: ProtocolCapabilitiesPayload(tcp: true, udp: true, udpMode: "stream"),
                    servers: [ServerPayload(name: "exit", address: "203.0.113.10:443", protocol: "trojan")]
                )
            ]
        )

        XCTAssertEqual(response.decision.ruleName, "ads")
        XCTAssertEqual(response.decision.action, "chain")
        XCTAssertEqual(response.decision.chainName, "proxy")
        XCTAssertEqual(response.chain?.capabilities.udpMode, "stream")
        XCTAssertEqual(response.hops.first?.name, "exit")
    }

    func testRuleTesterUsesDefaultChain() throws {
        let response = try RuleTester.test(
            network: "udp",
            target: "1.1.1.1:53",
            profile: "A",
            rules: [],
            chains: [
                ChainPayload(name: "proxy", hopCount: 1, servers: [ServerPayload(name: "dns", address: "203.0.113.10:443", protocol: "trojan")])
            ]
        )

        XCTAssertTrue(response.decision.isDefault)
        XCTAssertEqual(response.decision.action, "chain")
        XCTAssertEqual(response.decision.chainName, "proxy")
    }
}
