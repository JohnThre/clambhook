import XCTest
@testable import ClambhookShared

final class ConnectionMapTests: XCTestCase {
    func testMapPointsMergeSameLocationAndAggregate() {
        let london = LocationPayload(country: "United Kingdom", countryCode: "GB", city: "London", latitude: 51.5, longitude: -0.12)
        let traffic = TrafficSnapshotPayload(connections: [
            TrafficConnectionPayload(
                connID: "a",
                state: "active",
                ruleAction: "direct",
                targetHost: "alpha.example",
                geo: london,
                rxTotal: 100,
                txTotal: 10
            ),
            TrafficConnectionPayload(
                connID: "b",
                state: "closed",
                ruleAction: "block",
                targetHost: "beta.example",
                geo: london,
                rxTotal: 50,
                txTotal: 5
            ),
        ])

        let points = traffic.connectionMapPoints()

        XCTAssertEqual(points.count, 1)
        let point = try! XCTUnwrap(points.first)
        XCTAssertEqual(point.connectionCount, 2)
        XCTAssertEqual(point.activeCount, 1)
        XCTAssertEqual(point.rxTotal, 150)
        XCTAssertEqual(point.txTotal, 15)
        XCTAssertEqual(point.directCount, 1)
        XCTAssertEqual(point.blockCount, 1)
        XCTAssertEqual(point.locationName, "London, United Kingdom")
        XCTAssertEqual(Set(point.sampleHosts), ["alpha.example", "beta.example"])
    }

    func testMapPointsDropNullIslandAndInvalidCoordinates() {
        let traffic = TrafficSnapshotPayload(connections: [
            TrafficConnectionPayload(connID: "unresolved", targetHost: "no-geo.example", geo: LocationPayload()),
            TrafficConnectionPayload(
                connID: "bogus",
                targetHost: "bad.example",
                geo: LocationPayload(countryCode: "XX", latitude: 999, longitude: 999)
            ),
            TrafficConnectionPayload(
                connID: "real",
                targetHost: "tokyo.example",
                geo: LocationPayload(country: "Japan", countryCode: "JP", city: "Tokyo", latitude: 35.6, longitude: 139.7)
            ),
        ])

        let points = traffic.connectionMapPoints()

        XCTAssertEqual(points.map(\.id), ["JP/tokyo"])
    }

    func testMapPointsSortBusiestActiveFirst() {
        let traffic = TrafficSnapshotPayload(connections: [
            TrafficConnectionPayload(
                connID: "q1",
                state: "closed",
                geo: LocationPayload(countryCode: "US", city: "Quiet", latitude: 40, longitude: -100)
            ),
            TrafficConnectionPayload(
                connID: "b1",
                state: "active",
                geo: LocationPayload(countryCode: "DE", city: "Busy", latitude: 52, longitude: 13)
            ),
            TrafficConnectionPayload(
                connID: "b2",
                state: "active",
                geo: LocationPayload(countryCode: "DE", city: "Busy", latitude: 52, longitude: 13)
            ),
        ])

        let points = traffic.connectionMapPoints()

        XCTAssertEqual(points.map(\.id), ["DE/busy", "US/quiet"])
    }

    func testDominantActionFamilyPrefersBlockOnTie() {
        let point = ConnectionMapPoint(id: "x", latitude: 1, longitude: 1, proxyCount: 2, directCount: 0, blockCount: 2)
        XCTAssertEqual(point.dominantActionFamily, "block")
    }

    func testHasPlottableCoordinateRejectsOrigin() {
        XCTAssertFalse(LocationPayload(latitude: 0, longitude: 0).hasPlottableCoordinate)
        XCTAssertTrue(LocationPayload(latitude: 48.85, longitude: 2.35).hasPlottableCoordinate)
    }
}
