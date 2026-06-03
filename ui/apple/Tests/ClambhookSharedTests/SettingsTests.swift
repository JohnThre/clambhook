import XCTest
@testable import ClambhookShared

final class SettingsTests: XCTestCase {
    func testVPNDataUseDisclosureMatchesIPhoneMetadataOnlyPosture() {
        XCTAssertTrue(vpnDataUseDisclosure.contains("iPhone v1 inspection is metadata-only"))
        XCTAssertTrue(vpnDataUseDisclosure.contains("does not sell, use, or disclose VPN traffic data to third parties"))
        XCTAssertTrue(vpnDataUseDisclosure.contains("does not install a certificate authority"))
        XCTAssertTrue(vpnDataUseDisclosure.contains("perform TLS MITM"))
        XCTAssertTrue(vpnDataUseDisclosure.contains("export HAR files"))
        XCTAssertFalse(vpnDataUseDisclosure.contains("HTTPS body capture is opt-in"))
        XCTAssertFalse(vpnDataUseDisclosure.contains("body previews"))
    }
}
