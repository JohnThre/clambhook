import XCTest
@testable import ClambhookShared

@MainActor
final class AttentionStoreTests: XCTestCase {
    func testCaptureImportPersistsDecodedInboxItemAndPreview() throws {
        let url = temporaryAttentionURL("attention.json")
        let store = AttentionStore(fileURL: url)
        let toml = """
        active = "phone"
        password = "secret"

        [[profile]]
        name = "phone"

          [[profile.chain]]
          name = "proxy"

            [[profile.chain.server]]
            name = "exit"
            address = "vpn.example.net:443"
            protocol = "shadowsocks"

              [profile.chain.server.settings]
              method = "chacha20-ietf-poly1305"
        """

        let item = try store.captureImport(rawValue: toml, source: .clipboard)
        let reloaded = AttentionStore(fileURL: url)

        XCTAssertEqual(item.title, "phone")
        XCTAssertEqual(reloaded.state.inbox.count, 1)
        XCTAssertEqual(reloaded.state.inbox[0].preview.activeProfile, "phone")
        XCTAssertEqual(reloaded.state.inbox[0].preview.profileNames, ["phone"])
        XCTAssertEqual(reloaded.state.inbox[0].preview.serverCount, 1)
        XCTAssertFalse(reloaded.state.inbox[0].preview.redactedSnippet.contains("\"secret\""))
        XCTAssertTrue(reloaded.state.inbox[0].preview.redactedSnippet.contains("[redacted]"))
    }

    func testMoveInboxToSomedayAndRestore() throws {
        let store = AttentionStore()
        let item = try store.captureImport(rawValue: "active = \"A\"\n[[profile]]\nname = \"A\"\n", source: .qr)

        let someday = try XCTUnwrap(store.moveInboxItemToSomeday(id: item.id))

        XCTAssertTrue(store.state.inbox.isEmpty)
        XCTAssertEqual(store.state.someday.map(\.id), [someday.id])
        XCTAssertEqual(store.state.someday[0].configText, item.decodedConfigText)

        store.restoreSomedayItemToInbox(id: someday.id)

        XCTAssertTrue(store.state.someday.isEmpty)
        XCTAssertEqual(store.state.inbox.count, 1)
        XCTAssertEqual(store.state.inbox[0].decodedConfigText, item.decodedConfigText)
    }

    func testScheduledItemsClassifyDueUpcomingAndCompleteRecurrence() {
        let store = AttentionStore()
        let calendar = Calendar(identifier: .gregorian)
        let now = Date(timeIntervalSince1970: 1_700_000_000)
        let overdue = now.addingTimeInterval(-60)
        let future = now.addingTimeInterval(86_400)

        let oneShot = store.addScheduledItem(
            title: "Renew key",
            kind: .credentialRenewal,
            dueAt: overdue
        )
        let recurring = store.addScheduledItem(
            title: "Test exit",
            kind: .serverTest,
            dueAt: overdue,
            recurrence: .weekly
        )
        _ = store.addScheduledItem(
            title: "Later",
            kind: .serverTest,
            dueAt: future
        )

        XCTAssertEqual(store.dueScheduledItems(on: now).map(\.title), ["Renew key", "Test exit"])
        XCTAssertEqual(store.upcomingScheduledItems(after: now).map(\.title), ["Later"])

        store.completeScheduledItem(id: oneShot.id, completedAt: now, calendar: calendar)
        store.completeScheduledItem(id: recurring.id, completedAt: now, calendar: calendar)

        XCTAssertNotNil(store.state.scheduled.first { $0.id == oneShot.id }?.completedAt)
        XCTAssertEqual(
            store.state.scheduled.first { $0.id == recurring.id }?.dueAt,
            calendar.date(byAdding: .day, value: 7, to: overdue)
        )
    }
}

private func temporaryAttentionURL(_ name: String) -> URL {
    FileManager.default.temporaryDirectory
        .appendingPathComponent(UUID().uuidString, isDirectory: true)
        .appendingPathComponent(name)
}
