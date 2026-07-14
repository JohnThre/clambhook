import Foundation

public extension LocationPayload {
    /// A geolocation is plottable only when it carries a real coordinate.
    /// The daemon defaults unresolved lookups to (0, 0) ("null island"),
    /// so treat that — and any out-of-range value — as missing.
    var hasPlottableCoordinate: Bool {
        guard latitude.isFinite, longitude.isFinite else { return false }
        guard abs(latitude) <= 90, abs(longitude) <= 180 else { return false }
        return abs(latitude) > 0.0001 || abs(longitude) > 0.0001
    }

    /// Stable key used to merge connections that share a city/country so the
    /// map shows one pin per place instead of stacking coincident markers.
    var mapGroupingKey: String {
        let code = countryCode.trimmingCharacters(in: .whitespacesAndNewlines).uppercased()
        let cityName = city.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if !code.isEmpty && !cityName.isEmpty {
            return "\(code)/\(cityName)"
        }
        if !code.isEmpty {
            return code
        }
        return String(format: "%.2f,%.2f", latitude, longitude)
    }
}

/// A single map pin aggregating every connection observed at one location.
public struct ConnectionMapPoint: Identifiable, Equatable, Sendable {
    public var id: String
    public var latitude: Double
    public var longitude: Double
    public var city: String
    public var country: String
    public var countryCode: String
    public var connectionCount: Int
    public var activeCount: Int
    public var rxTotal: UInt64
    public var txTotal: UInt64
    public var proxyCount: Int
    public var directCount: Int
    public var blockCount: Int
    public var sampleHosts: [String]

    public init(
        id: String,
        latitude: Double,
        longitude: Double,
        city: String = "",
        country: String = "",
        countryCode: String = "",
        connectionCount: Int = 0,
        activeCount: Int = 0,
        rxTotal: UInt64 = 0,
        txTotal: UInt64 = 0,
        proxyCount: Int = 0,
        directCount: Int = 0,
        blockCount: Int = 0,
        sampleHosts: [String] = []
    ) {
        self.id = id
        self.latitude = latitude
        self.longitude = longitude
        self.city = city
        self.country = country
        self.countryCode = countryCode
        self.connectionCount = connectionCount
        self.activeCount = activeCount
        self.rxTotal = rxTotal
        self.txTotal = txTotal
        self.proxyCount = proxyCount
        self.directCount = directCount
        self.blockCount = blockCount
        self.sampleHosts = sampleHosts
    }

    /// Routing decision that dominates this location, driving the pin color.
    /// Blocks take precedence on ties so a hostile place never reads as safe.
    public var dominantActionFamily: String {
        if blockCount > 0 && blockCount >= proxyCount && blockCount >= directCount {
            return "block"
        }
        if directCount > proxyCount && directCount >= blockCount {
            return "direct"
        }
        return "proxy"
    }

    public var locationName: String {
        let cityName = city.trimmingCharacters(in: .whitespacesAndNewlines)
        let countryName = country.trimmingCharacters(in: .whitespacesAndNewlines)
        if !cityName.isEmpty && !countryName.isEmpty {
            return "\(cityName), \(countryName)"
        }
        if !countryName.isEmpty {
            return countryName
        }
        if !cityName.isEmpty {
            return cityName
        }
        return "Unknown location"
    }
}

public extension TrafficSnapshotPayload {
    /// Collapses the live connection list into one pin per geolocated place,
    /// sorted by busiest first. Connections without a real coordinate are
    /// dropped so the map never plots "null island".
    func connectionMapPoints(maxSampleHosts: Int = 5) -> [ConnectionMapPoint] {
        var points: [String: ConnectionMapPoint] = [:]
        var order: [String] = []

        for connection in connections {
            let geo = connection.geo
            guard geo.hasPlottableCoordinate else { continue }
            let key = geo.mapGroupingKey

            if points[key] == nil {
                points[key] = ConnectionMapPoint(
                    id: key,
                    latitude: geo.latitude,
                    longitude: geo.longitude,
                    city: geo.city,
                    country: geo.country,
                    countryCode: geo.countryCode
                )
                order.append(key)
            }

            points[key]?.connectionCount += 1
            if connection.state.lowercased() == "active" {
                points[key]?.activeCount += 1
            }
            points[key]?.rxTotal += connection.rxTotal
            points[key]?.txTotal += connection.txTotal

            switch connection.actionFamily {
            case "block":
                points[key]?.blockCount += 1
            case "direct":
                points[key]?.directCount += 1
            default:
                points[key]?.proxyCount += 1
            }

            let host = connection.monitorHost
            if !host.isEmpty,
               var point = points[key],
               point.sampleHosts.count < maxSampleHosts,
               !point.sampleHosts.contains(host) {
                point.sampleHosts.append(host)
                points[key] = point
            }
        }

        return order.compactMap { points[$0] }
            .sorted {
                if $0.activeCount != $1.activeCount {
                    return $0.activeCount > $1.activeCount
                }
                return $0.connectionCount > $1.connectionCount
            }
    }
}
