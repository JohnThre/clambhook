import XCTest
@testable import ClambhookShared

final class SettingsTests: XCTestCase {
    func testVPNDataUseDisclosureSeparatesActivityAndOptInHTTPCapture() {
        XCTAssertTrue(vpnDataUseDisclosure.contains("Activity inspection is metadata-only by default"))
        XCTAssertTrue(vpnDataUseDisclosure.contains("does not sell, use, or disclose VPN traffic data to third parties"))
        XCTAssertTrue(vpnDataUseDisclosure.contains("HTTP Capture is a separate local opt-in"))
        XCTAssertTrue(vpnDataUseDisclosure.contains("user-trusted local certificate authority"))
        XCTAssertTrue(vpnDataUseDisclosure.contains("bounded request and response body previews"))
        XCTAssertTrue(vpnDataUseDisclosure.contains("HAR exports on this Mac"))
        XCTAssertTrue(vpnDataUseDisclosure.contains("captures stay on this device unless you export them"))
    }

    func testDeveloperCaptureDisclosureSeparatesHTTPAndHTTPSCapture() {
        XCTAssertTrue(developerCaptureDisclosure.contains("HTTP Capture is opt-in and local"))
        XCTAssertTrue(developerCaptureDisclosure.contains("configured query parameters are redacted"))
        XCTAssertFalse(developerCaptureDisclosure.contains("creates a local certificate authority"))

        XCTAssertTrue(developerHTTPSCaptureDisclosure.contains("HTTPS capture is a separate opt-in"))
        XCTAssertTrue(developerHTTPSCaptureDisclosure.contains("trust that CA in your user keychain"))
        XCTAssertTrue(developerHTTPSCaptureDisclosure.contains("Only enable it for devices and test traffic you control"))
    }

    func testHARExportDisclosureWarnsBeforeSharing() {
        XCTAssertTrue(developerHARExportDisclosure.contains("HAR exports can include URLs"))
        XCTAssertTrue(developerHARExportDisclosure.contains("Review the file before sharing"))
    }

    func testMacOSProxyScopeDisclosureStatesSystemProxyAndEnhancedMode() {
        XCTAssertTrue(macOSProxyScopeDisclosure.contains("System Proxy mode"))
        XCTAssertTrue(macOSProxyScopeDisclosure.contains("Enhanced Mode"))
        XCTAssertTrue(macOSProxyScopeDisclosure.contains("utun"))
        XCTAssertTrue(macOSProxyScopeDisclosure.contains("device-wide routing"))
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
        XCTAssertEqual(settings.routingMode, .systemProxy)
        XCTAssertTrue(settings.usePrivilegedHelper)
        XCTAssertEqual(settings.updateChannel, "stable")
        XCTAssertEqual(settings.updateManifestURL, defaultStableUpdateManifestURL)
    }

    func testLegacyRoutingModesNormalizeToSystemProxy() throws {
        for raw in ["network_extension", "daemon_proxy"] {
            let data = Data(#"{"routingMode":"\#(raw)"}"#.utf8)
            let settings = try JSONDecoder().decode(AppSettings.self, from: data).normalized()
            XCTAssertEqual(settings.routingMode, .systemProxy)
        }
    }

    func testEnhancedModeForcesPrivilegedHelper() {
        let settings = AppSettings(
            routingMode: .enhancedTUN,
            usePrivilegedHelper: false
        ).normalized()

        XCTAssertTrue(settings.usePrivilegedHelper)
    }

    func testDefaultUpdateManifestURLsUseReleaseAPIEndpoints() {
        XCTAssertEqual(
            defaultStableUpdateManifestURL.absoluteString,
            "https://store.clambercloud.com/api/clambhook/update-manifest"
        )
        XCTAssertEqual(
            defaultBetaUpdateManifestURL.absoluteString,
            "https://store.clambercloud.com/api/clambhook/update-manifest?channel=beta"
        )
    }

    func testNormalizingLegacyManifestURLsMigratesToReleaseAPIEndpoints() {
        let settings = AppSettings(
            stableUpdateManifestURL: URL(string: "https://jpfchang.org/clambhook/clambhook-update-manifest.json")!,
            betaUpdateManifestURL: URL(string: "https://jpfchang.org/clambhook/clambhook-beta-update-manifest.json")!
        ).normalized()

        XCTAssertEqual(settings.stableUpdateManifestURL, defaultStableUpdateManifestURL)
        XCTAssertEqual(settings.betaUpdateManifestURL, defaultBetaUpdateManifestURL)
    }

    func testNormalizingLegacyBetaManifestURLWithChannelQueryMigratesToReleaseAPIEndpoint() {
        let settings = AppSettings(
            betaUpdateManifestURL: URL(string: "https://jpfchang.org/clambhook/clambhook-update-manifest.json?channel=beta")!
        ).normalized()

        XCTAssertEqual(settings.betaUpdateManifestURL, defaultBetaUpdateManifestURL)
    }

    func testNormalizingLegacyReleaseAPIURLsMigratesToStoreEndpoints() {
        let settings = AppSettings(
            stableUpdateManifestURL: URL(string: "https://jpfchang.org/api/clambhook/update-manifest")!,
            betaUpdateManifestURL: URL(string: "https://jpfchang.org/api/clambhook/update-manifest?channel=beta")!
        ).normalized()

        XCTAssertEqual(settings.stableUpdateManifestURL, defaultStableUpdateManifestURL)
        XCTAssertEqual(settings.betaUpdateManifestURL, defaultBetaUpdateManifestURL)
    }

    func testNormalizingCustomManifestURLsKeepsSupportedEndpoints() {
        let stableURL = URL(string: "https://updates.example.com/clambhook/stable.json")!
        let betaURL = URL(string: "https://updates.example.com/clambhook/beta.json")!
        let settings = AppSettings(
            stableUpdateManifestURL: stableURL,
            betaUpdateManifestURL: betaURL
        ).normalized()

        XCTAssertEqual(settings.stableUpdateManifestURL, stableURL)
        XCTAssertEqual(settings.betaUpdateManifestURL, betaURL)
    }

    func testMacOSIdentifiersUseJPFChangNamespace() {
        XCTAssertEqual(clambhookMacAppBundleIdentifier, "org.jpfchang.clambhook.mac")
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
