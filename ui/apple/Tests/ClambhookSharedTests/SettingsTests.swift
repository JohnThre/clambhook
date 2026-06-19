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

    func testMacOSProxyScopeDisclosureStatesNetworkExtensionAndProxyFallback() {
        XCTAssertTrue(macOSProxyScopeDisclosure.contains("Network Extension mode"))
        XCTAssertTrue(macOSProxyScopeDisclosure.contains("packet tunnel"))
        XCTAssertTrue(macOSProxyScopeDisclosure.contains("device-wide routing"))
        XCTAssertTrue(macOSProxyScopeDisclosure.contains("System proxy mode"))
        XCTAssertTrue(macOSProxyScopeDisclosure.contains("HTTP, HTTPS, and SOCKS proxy settings"))
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
        XCTAssertEqual(settings.routingMode, .networkExtension)
        XCTAssertTrue(settings.usePrivilegedHelper)
        XCTAssertEqual(settings.updateChannel, "stable")
        XCTAssertEqual(settings.updateManifestURL, defaultStableUpdateManifestURL)
    }

    func testMacOSIdentifiersUseJPFChangNamespace() {
        XCTAssertEqual(clambhookMacAppBundleIdentifier, "org.jpfchang.clambhook.mac")
        XCTAssertEqual(clambhookMacTunnelBundleIdentifier, "org.jpfchang.clambhook.mac.tunnel")
        XCTAssertEqual(clambhookMacWidgetBundleIdentifier, "org.jpfchang.clambhook.mac.widgets")
        XCTAssertEqual(clambhookMacPrivilegedHelperLabel, "org.jpfchang.clambhook.mac.helper")
        XCTAssertEqual(clambhookMacPrivilegedHelperPlistName, "org.jpfchang.clambhook.mac.helper.plist")
        XCTAssertEqual(defaultAppleKeychainAccessGroup, "V6GG4HYABJ.org.jpfchang.clambhook")
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
