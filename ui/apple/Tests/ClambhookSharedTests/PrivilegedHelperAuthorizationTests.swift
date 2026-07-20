import XCTest
@testable import ClambhookShared

final class PrivilegedHelperAuthorizationTests: XCTestCase {
    func testClientCodeRequirementPinsTeamAndIdentifier() {
        let requirement = PrivilegedHelperClientAuthorization.clientCodeRequirement(
            teamIdentifier: "V6GG4HYABJ",
            clientIdentifier: "org.jpfchang.clambhook.mac"
        )
        XCTAssertEqual(
            requirement,
            "anchor apple generic and certificate leaf[subject.OU] = \"V6GG4HYABJ\" and identifier \"org.jpfchang.clambhook.mac\""
        )
    }

    #if canImport(Darwin)
    func testAuditTokenSerializesTo32BytesAndRoundTrips() {
        let token = audit_token_t(val: (1, 2, 3, 4, 5, 6, 7, 8))
        let data = PrivilegedHelperClientAuthorization.auditTokenData(token)
        XCTAssertEqual(data.count, 32)

        var restored = audit_token_t(val: (0, 0, 0, 0, 0, 0, 0, 0))
        withUnsafeMutableBytes(of: &restored) { raw in
            data.copyBytes(to: raw.bindMemory(to: UInt8.self))
        }
        XCTAssertEqual(restored.val.0, 1)
        XCTAssertEqual(restored.val.7, 8)
    }
    #endif

    #if canImport(Security) && canImport(Darwin)
    func testAuditGuestAttributesUseAuditKeyNotPID() {
        let token = audit_token_t(val: (9, 8, 7, 6, 5, 4, 3, 2))
        let attrs = PrivilegedHelperClientAuthorization.auditGuestAttributes(token)
        XCTAssertNotNil(attrs[kSecGuestAttributeAudit as String] as? Data)
        XCTAssertNil(attrs[kSecGuestAttributePid as String])
        XCTAssertEqual((attrs[kSecGuestAttributeAudit as String] as? Data)?.count, 32)
    }
    #endif
}
