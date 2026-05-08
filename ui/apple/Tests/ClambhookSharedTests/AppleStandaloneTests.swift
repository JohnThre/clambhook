import XCTest
@testable import ClambhookShared

final class AppleStandaloneTests: XCTestCase {
    func testAppleBundleIdentifiersUseJpfchangNamespace() {
        XCTAssertEqual(appleIOSBundleIdentifier, "org.jpfchang.clambhook.ios")
        XCTAssertEqual(appleIOSWidgetBundleIdentifier, "org.jpfchang.clambhook.ios.widgets")
        XCTAssertEqual(appleIOSPacketTunnelBundleIdentifier, "org.jpfchang.clambhook.ios.packet-tunnel")
        XCTAssertEqual(defaultAppGroupIdentifier, "group.org.jpfchang.clambhook.shared")
    }

    @MainActor
    func testStandaloneConfigStorePersistsRawTOMLAndActiveProfile() throws {
        let suiteName = "standalone-config-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suiteName)!
        defer { defaults.removePersistentDomain(forName: suiteName) }

        let store = StandaloneConfigStore(defaults: defaults)
        let document = StandaloneConfigDocument(
            toml: """
            active = "work"

            [[profile]]
            name = "work"
            """,
            activeProfile: "work"
        )

        try store.save(document)

        XCTAssertEqual(store.document.toml, document.toml)
        XCTAssertEqual(store.document.activeProfile, "work")
        XCTAssertEqual(StandaloneConfigStore(defaults: defaults).document, document)
    }

    @MainActor
    func testStandaloneConfigStoreRejectsEmptyTOML() {
        let store = StandaloneConfigStore(defaults: .standard, autosave: false)
        XCTAssertThrowsError(try store.save(StandaloneConfigDocument(toml: "   ", activeProfile: ""))) { error in
            XCTAssertEqual(error as? StandaloneConfigError, .emptyConfig)
        }
    }

}
