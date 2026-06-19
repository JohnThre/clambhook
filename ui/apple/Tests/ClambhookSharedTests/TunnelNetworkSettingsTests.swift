import XCTest
@testable import ClambhookShared

final class TunnelNetworkSettingsTests: XCTestCase {
    func testFullTunnelRoutesRequireIPv4AndIPv6Defaults() {
        XCTAssertTrue(TunnelNetworkSettingsPayload(
            includedRoutes: ["0.0.0.0/0", "::/0"]
        ).usesFullTunnelRoutes)
        XCTAssertFalse(TunnelNetworkSettingsPayload(
            includedRoutes: ["0.0.0.0/0"]
        ).usesFullTunnelRoutes)
        XCTAssertFalse(TunnelNetworkSettingsPayload(
            includedRoutes: ["10.0.0.0/8", "fd00::/8"]
        ).usesFullTunnelRoutes)
    }

    func testRoutePrefixParsesIPv4AndIPv6Routes() throws {
        let ipv4 = try TunnelRoutePrefix("10.0.0.0/8")
        XCTAssertTrue(ipv4.isIPv4)
        XCTAssertEqual(ipv4.address, "10.0.0.0")
        XCTAssertEqual(ipv4.prefixLen, 8)
        XCTAssertFalse(ipv4.isDefaultRoute)

        let ipv4Default = try TunnelRoutePrefix("0.0.0.0/0")
        XCTAssertTrue(ipv4Default.isIPv4DefaultRoute)

        let ipv6 = try TunnelRoutePrefix("fd00::/8")
        XCTAssertTrue(ipv6.isIPv6)
        XCTAssertEqual(ipv6.address, "fd00::")
        XCTAssertEqual(ipv6.prefixLen, 8)
        XCTAssertFalse(ipv6.isDefaultRoute)

        let ipv6Default = try TunnelRoutePrefix("::/0")
        XCTAssertTrue(ipv6Default.isIPv6DefaultRoute)
    }

    func testRoutePrefixRejectsMalformedCIDRs() {
        XCTAssertThrowsError(try TunnelRoutePrefix("10.0.0.0"))
        XCTAssertThrowsError(try TunnelRoutePrefix("10.0.0.0/33"))
        XCTAssertThrowsError(try TunnelRoutePrefix("10.0.0.999/24"))
        XCTAssertThrowsError(try TunnelRoutePrefix("fd00::/129"))
        XCTAssertThrowsError(try TunnelRoutePrefix("not-a-route/24"))
    }

    func testPayloadRoutePrefixParsingIncludesExcludedRoutes() throws {
        let payload = TunnelNetworkSettingsPayload(
            includedRoutes: ["0.0.0.0/0", "::/0"],
            excludedRoutes: ["127.0.0.0/8", "::1/128"]
        )

        XCTAssertEqual(try payload.includedRoutePrefixes().map(\.rawValue), ["0.0.0.0/0", "::/0"])
        XCTAssertEqual(try payload.excludedRoutePrefixes().map(\.rawValue), ["127.0.0.0/8", "::1/128"])
    }
}
