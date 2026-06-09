import XCTest
@testable import ClambhookShared

final class CaptureSupportTests: XCTestCase {
    func testCaptureEntriesDeriveHTTPAndHTTPSVisibility() {
        let traffic = TrafficSnapshotPayload(connections: [
            TrafficConnectionPayload(
                connID: "http-1",
                state: "closed",
                ruleAction: "direct",
                target: "example.com:80",
                targetHost: "example.com",
                targetPort: "80",
                timeline: [
                    TrafficTimelinePayload(tsNs: 10, type: "connection.opened", title: "Opened", detail: "http client"),
                    TrafficTimelinePayload(tsNs: 20, type: "connection.established", title: "Connected", detail: "1ms"),
                ],
                visibility: TrafficVisibilityPayload(
                    kind: "http",
                    method: "GET",
                    scheme: "http",
                    host: "example.com",
                    port: "80",
                    path: "/path"
                )
            ),
            TrafficConnectionPayload(
                connID: "https-1",
                state: "active",
                chainName: "proxy",
                target: "secure.example:443",
                targetHost: "secure.example",
                targetPort: "443",
                visibility: TrafficVisibilityPayload(
                    kind: "http_connect",
                    method: "CONNECT",
                    scheme: "https",
                    host: "secure.example",
                    port: "443"
                )
            ),
            TrafficConnectionPayload(connID: "tcp-1", target: "1.1.1.1:853"),
        ])

        let entries = CaptureSupport.captureEntries(from: traffic)

        XCTAssertEqual(entries.map { $0.id }.sorted(), ["http-1", "https-1"])
        XCTAssertEqual(entries.first { $0.id == "http-1" }?.sslState, "not_tls")
        XCTAssertEqual(entries.first { $0.id == "https-1" }?.sslState, "metadata_only")
        XCTAssertEqual(entries.first { $0.id == "https-1" }?.requestBody.available, false)
        XCTAssertEqual(entries.first { $0.id == "http-1" }?.timeline.map { $0.title }, ["Opened", "Connected"])
    }

    func testCaptureFiltersGroupingAndMetadataOnlyExport() throws {
        let entries = [
            CaptureEntryPayload(
                id: "a",
                updatedAtNs: 20,
                state: "closed",
                method: "GET",
                scheme: "http",
                host: "alpha.example",
                path: "/index",
                sslState: "not_tls",
                ruleAction: "direct",
                timeline: [TrafficTimelinePayload(tsNs: 20, type: "connection.closed", title: "Closed", detail: "client_eof")]
            ),
            CaptureEntryPayload(
                id: "b",
                updatedAtNs: 30,
                state: "active",
                method: "CONNECT",
                scheme: "https",
                host: "beta.example",
                sslState: "metadata_only",
                ruleAction: "block"
            ),
            CaptureEntryPayload(
                id: "c",
                updatedAtNs: 10,
                state: "closed",
                method: "GET",
                scheme: "http",
                host: "ALPHA.example.",
                sslState: "not_tls",
                ruleAction: "chain:work",
                requestBody: CaptureBodyPayload(available: true, preview: "hello")
            ),
        ]

        XCTAssertEqual(CaptureSupport.filteredEntries(entries, filter: .active).map(\.id), ["b"])
        XCTAssertEqual(CaptureSupport.filteredEntries(entries, filter: .https).map(\.id), ["b"])
        XCTAssertEqual(CaptureSupport.filteredEntries(entries, filter: .direct).map(\.id), ["a"])
        XCTAssertEqual(CaptureSupport.filteredEntries(entries, filter: .block).map(\.id), ["b"])
        XCTAssertEqual(CaptureSupport.filteredEntries(entries, filter: .proxy).map(\.id), ["c"])
        XCTAssertEqual(CaptureSupport.filteredEntries(entries, filter: .all, query: "beta").map(\.id), ["b"])
        XCTAssertEqual(CaptureSupport.filteredEntries(entries, filter: .pinned, pinnedIDs: ["c"]).map(\.id), ["c"])

        let groups = CaptureSupport.groupEntriesByHost(entries)
        XCTAssertEqual(groups.map { $0.key }, ["beta.example", "alpha.example"])
        XCTAssertEqual(groups.first { $0.key == "alpha.example" }?.count, 2)

        let pinnedGroups = CaptureSupport.groupEntriesByHost(entries, pinnedIDs: ["c"])
        XCTAssertEqual(pinnedGroups.map { $0.key }, ["alpha.example", "beta.example"])
        XCTAssertEqual(pinnedGroups.first?.entries.map { $0.id }, ["c", "a"])

        let export = CaptureSupport.exportString(traffic: TrafficSnapshotPayload(), entries: entries, generatedAt: Date(timeIntervalSince1970: 0))
        XCTAssertTrue(export.contains(#""groups""#))
        XCTAssertTrue(export.contains(#""timeline""#))
        XCTAssertTrue(export.contains("Closed"))
        XCTAssertTrue(export.contains("metadata_only"))
        XCTAssertFalse(export.contains("request_body"))
        XCTAssertFalse(export.contains("response_body"))
        XCTAssertFalse(export.contains("preview"))
        XCTAssertFalse(export.contains("certificate"))
        XCTAssertFalse(export.lowercased().contains("mitm"))
        XCTAssertFalse(export.lowercased().contains("\"har\""))
        XCTAssertFalse(export.contains("HAR export"))
        XCTAssertFalse(export.lowercased().contains("pinned"))

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let snapshot = try decoder.decode(CaptureSnapshotPayload.self, from: Data(export.utf8))
        XCTAssertEqual(snapshot.groups.first?.key, "beta.example")
        XCTAssertEqual(snapshot.entries.first?.id, "a")
    }
}
