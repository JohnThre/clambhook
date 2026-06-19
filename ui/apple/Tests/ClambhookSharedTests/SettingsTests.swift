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

    func testMacOSProxyScopeDisclosureStatesProxyOnlyBoundary() {
        XCTAssertTrue(macOSProxyScopeDisclosure.contains("HTTP, HTTPS, and SOCKS system proxy"))
        XCTAssertTrue(macOSProxyScopeDisclosure.contains("apps that honor those proxy settings"))
        XCTAssertTrue(macOSProxyScopeDisclosure.contains("not a packet tunnel"))
        XCTAssertTrue(macOSProxyScopeDisclosure.contains("full-device VPN"))
        XCTAssertTrue(macOSProxyScopeDisclosure.contains("DNS interceptor"))
    }

    func testSettingsDecodeNewMacDefaultsFromOldPayload() throws {
        let data = Data("""
        {
          "apiEndpoint": "http://127.0.0.1:9090",
          "daemonBinaryPath": "",
          "daemonConfigPath": "",
          "launchDaemonOnStart": true,
          "stopDaemonOnQuit": true,
          "refreshIntervalSeconds": 2,
          "logRetention": 200,
          "appGroupIdentifier": "group.org.jpfchang.clambhook"
        }
        """.utf8)

        let settings = try JSONDecoder().decode(AppSettings.self, from: data).normalized()

        XCTAssertFalse(settings.systemProxyEnabled)
        XCTAssertEqual(settings.updateChannel, "stable")
        XCTAssertEqual(settings.updateManifestURL, defaultStableUpdateManifestURL)
    }

    func testUpdateComparatorUsesVersionThenBuild() {
        XCTAssertTrue(MacUpdateComparator.isUpdateAvailable(
            currentVersion: "1.0",
            currentBuild: "10",
            manifest: MacUpdateManifest(version: "1.1", build: "1")
        ))
        XCTAssertTrue(MacUpdateComparator.isUpdateAvailable(
            currentVersion: "1.0",
            currentBuild: "10",
            manifest: MacUpdateManifest(version: "1.0", build: "11")
        ))
        XCTAssertFalse(MacUpdateComparator.isUpdateAvailable(
            currentVersion: "1.0",
            currentBuild: "10",
            manifest: MacUpdateManifest(version: "1.0", build: "10")
        ))
    }
}
