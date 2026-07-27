import Testing
import Foundation
@testable import ClambhookUI

@Suite struct ClambhookUITests {

    @Test func decodeType() throws {
        // load the TestData.json file from the Resources folder and decode it into a struct
        let resourceURL: URL = try #require(Bundle.module.url(forResource: "TestData", withExtension: "json"))
        let testData = try JSONDecoder().decode(TestData.self, from: Data(contentsOf: resourceURL))
        #expect(testData.testModuleName == "ClambhookUI")
    }

    @Test func defaultStatusIsDisconnected() throws {
        let status = TunnelStatus()
        #expect(status.running == false)
        #expect(status.connectionStateLabel == "Not Connected")
        #expect(status.profileLabel == "No profile")
        #expect(status.activeConnections == 0)
    }

    @Test func runningStatusReportsConnectedAndProfile() throws {
        let status = TunnelStatus(
            running: true,
            profileName: "Work",
            activeConnections: 3,
            downloadBytesPerSecond: 2048,
            uploadBytesPerSecond: 512
        )
        #expect(status.connectionStateLabel == "Connected")
        #expect(status.profileLabel == "Work")
        #expect(status.activeConnections == 3)
    }

    @Test func emptyProfileNameFallsBackToPlaceholder() throws {
        #expect(TunnelStatus(profileName: "").profileLabel == "No profile")
        #expect(TunnelStatus(profileName: "Home").profileLabel == "Home")
    }

    @Test func formatByteRateScalesUnits() throws {
        #expect(formatByteRate(0) == "0 B/s")
        #expect(formatByteRate(512) == "512 B/s")
        #expect(formatByteRate(1023) == "1023 B/s")
        #expect(formatByteRate(1024) == "1.0 KB/s")
        #expect(formatByteRate(1536) == "1.5 KB/s")
        #expect(formatByteRate(1_048_576) == "1.0 MB/s")
        #expect(formatByteRate(1_073_741_824) == "1.0 GB/s")
    }

    @Test func formatByteRateTreatsInvalidRatesAsZero() throws {
        // max(0, .nan) is .nan and Int(.nan) traps; guard against negatives/NaN/inf.
        #expect(formatByteRate(-5) == "0 B/s")
        #expect(formatByteRate(.nan) == "0 B/s")
        #expect(formatByteRate(.infinity) == "0 B/s")
    }

}

struct TestData : Codable, Hashable {
    var testModuleName: String
}
