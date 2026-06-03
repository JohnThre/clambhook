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

        XCTAssertEqual(entries.map(\.id).sorted(), ["http-1", "https-1"])
        XCTAssertEqual(entries.first { $0.id == "http-1" }?.sslState, "not_tls")
        XCTAssertEqual(entries.first { $0.id == "https-1" }?.sslState, "metadata_only")
        XCTAssertEqual(entries.first { $0.id == "https-1" }?.requestBody.available, false)
    }

    func testCaptureFiltersAndExportNote() {
        let entries = [
            CaptureEntryPayload(id: "a", method: "GET", scheme: "http", host: "alpha.example", sslState: "not_tls"),
            CaptureEntryPayload(id: "b", method: "CONNECT", scheme: "https", host: "beta.example", sslState: "metadata_only"),
            CaptureEntryPayload(
                id: "c",
                method: "GET",
                scheme: "https",
                host: "gamma.example",
                sslState: "decrypted",
                requestBody: CaptureBodyPayload(available: true, preview: "hello")
            ),
        ]

        XCTAssertEqual(CaptureSupport.filteredEntries(entries, filter: .https).map(\.id), ["b", "c"])
        XCTAssertEqual(CaptureSupport.filteredEntries(entries, filter: .metadataOnly).map(\.id), ["a", "b"])
        XCTAssertEqual(CaptureSupport.filteredEntries(entries, filter: .all, query: "beta").map(\.id), ["b"])

        let export = CaptureSupport.exportString(traffic: TrafficSnapshotPayload(), entries: entries, generatedAt: Date(timeIntervalSince1970: 0))
        XCTAssertTrue(export.contains("HTTPS Body Capture"))
        XCTAssertTrue(export.contains("developer capture mode"))
        XCTAssertTrue(export.contains("metadata_only"))
    }
}
